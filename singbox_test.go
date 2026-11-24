package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

const sampleURI = "vless://00000000-0000-0000-0000-000000000000@server.example:443?type=ws&security=tls&path=%2Fvless&sni=sni.example&host=host.example#Test"

// serverHostRoute adalah route host untuk p.Address di decodeConfig.
const serverHostRoute = "203.0.113.10/32"

func decodeConfig(t *testing.T, modern bool) map[string]any {
	t.Helper()
	p, err := parseVLESS(sampleURI)
	if err != nil {
		t.Fatal(err)
	}
	// start() mengunci alamat server ke IP sebelum tunnel naik.
	p.Address = "203.0.113.10"
	raw, err := buildSingboxConfig(p, 443, modern)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func dict(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("bukan object: %#v", v)
	}
	return m
}

func list(t *testing.T, v any) []any {
	t.Helper()
	l, ok := v.([]any)
	if !ok {
		t.Fatalf("bukan array: %#v", v)
	}
	return l
}

func stringsOf(t *testing.T, v any) []string {
	t.Helper()
	var out []string
	for _, item := range list(t, v) {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("bukan string: %#v", item)
		}
		out = append(out, s)
	}
	return out
}

// The whole point of the desktop client: nothing may answer DNS outside the
// tunnel, and TUN routing must be strict with LAN carved out.
func TestConfigLeakProtection(t *testing.T) {
	cfg := decodeConfig(t, false)

	inbounds := list(t, cfg["inbounds"])
	if len(inbounds) != 1 {
		t.Fatalf("desktop harus TUN saja, dapat %d inbound", len(inbounds))
	}
	tun := dict(t, inbounds[0])
	if tun["type"] != "tun" || tun["interface_name"] != tunInterfaceName {
		t.Fatalf("inbound salah: %#v", tun)
	}
	if tun["strict_route"] != true || tun["auto_route"] != true {
		t.Fatal("strict_route/auto_route harus aktif: itu yang menahan DNS keluar TUN")
	}
	addrs := stringsOf(t, tun["address"])
	if !contains(addrs, tunAddress4) || !contains(addrs, tunAddress6) {
		t.Fatalf("alamat TUN kurang: %v", addrs)
	}
	excluded := stringsOf(t, tun["route_exclude_address"])
	for _, want := range []string{"192.168.0.0/16", "10.0.0.0/8", serverHostRoute} {
		if !contains(excluded, want) {
			t.Fatalf("route_exclude_address harus memuat %s (LAN + server VPN): %v", want, excluded)
		}
	}

	// DNS: tidak boleh ada resolver di luar tunnel, termasuk resolver lokal.
	dns := dict(t, cfg["dns"])
	if dns["final"] != "dns-tunnel" {
		t.Fatalf("resolver final harus lewat tunnel, dapat %v", dns["final"])
	}
	servers := list(t, dns["servers"])
	if len(servers) != 2 {
		t.Fatalf("harus tepat dua resolver (semuanya di tunnel), dapat %d", len(servers))
	}
	for _, raw := range servers {
		server := dict(t, raw)
		if server["detour"] != "proxy" {
			t.Fatalf("resolver %v tidak lewat tunnel: %#v", server["tag"], server)
		}
		if server["address"] == "local" || server["type"] == "local" {
			t.Fatalf("resolver lokal membuka jalan DNS keluar tunnel: %#v", server)
		}
	}

	route := dict(t, cfg["route"])
	if route["final"] != "proxy" {
		t.Fatalf("default route harus lewat proxy, dapat %v", route["final"])
	}
	hasHijack := false
	serverDirect := false
	for _, raw := range list(t, route["rules"]) {
		rule := dict(t, raw)
		if rule["action"] == "hijack-dns" && rule["protocol"] == "dns" {
			hasHijack = true
		}
		if rule["outbound"] == "direct" {
			if cidrs := rule["ip_cidr"]; cidrs != nil && contains(stringsOf(t, cidrs), serverHostRoute) {
				serverDirect = true
			}
		}
	}
	if !hasHijack {
		t.Fatal("tanpa aturan hijack-dns, DNS bisa keluar TUN")
	}
	if !serverDirect {
		t.Fatal("IP server VPN harus direct supaya tidak jadi routing loop")
	}
}

// The legacy dialect is what the bundled core (sing-box 1.11.x) accepts; the
// modern one is required from 1.12 on. Both contracts were verified against
// real cores, so the field sets are pinned here.
func TestConfigDialects(t *testing.T) {
	legacy := decodeConfig(t, false)
	dnsLegacy := dict(t, legacy["dns"])
	if dnsLegacy["strategy"] != "prefer_ipv4" || dnsLegacy["independent_cache"] != true {
		t.Fatal("dialek legacy butuh dns.strategy + independent_cache")
	}
	if dict(t, list(t, dnsLegacy["servers"])[0])["address"] == nil {
		t.Fatal("dialek legacy memakai field address")
	}
	outLegacy := dict(t, list(t, legacy["outbounds"])[0])
	if outLegacy["domain_strategy"] != "prefer_ipv4" || outLegacy["domain_resolver"] != nil {
		t.Fatalf("outbound legacy salah: %#v", outLegacy)
	}

	modern := decodeConfig(t, true)
	dnsModern := dict(t, modern["dns"])
	if _, ok := dnsModern["strategy"]; ok {
		t.Fatal("dns.strategy sudah dihapus di sing-box 1.14")
	}
	if _, ok := dnsModern["independent_cache"]; ok {
		t.Fatal("independent_cache sudah dihapus di sing-box 1.16/1.14")
	}
	first := dict(t, list(t, dnsModern["servers"])[0])
	if first["type"] != "udp" || first["server"] != "1.1.1.1" {
		t.Fatalf("dialek modern memakai field type/server: %#v", first)
	}
	outModern := dict(t, list(t, modern["outbounds"])[0])
	if _, ok := outModern["domain_strategy"]; ok {
		t.Fatal("domain_strategy dihapus di sing-box 1.14")
	}
	routeModern := dict(t, modern["route"])
	if dict(t, routeModern["default_domain_resolver"])["server"] != "dns-tunnel" {
		t.Fatal("dialek modern butuh route.default_domain_resolver")
	}
}

// Alamat server harus jadi route host tunggal, supaya koneksi ke server tidak
// ikut masuk TUN (dan hostname tidak pernah diresolusi di dalam tunnel).
func TestHostRoute(t *testing.T) {
	cases := map[string]string{
		"203.0.113.10":   "203.0.113.10/32",
		"2001:db8::1":    "2001:db8::1/128",
		"server.example": "",
		"":               "",
	}
	for addr, want := range cases {
		if got := hostRoute(addr); got != want {
			t.Fatalf("hostRoute(%q) = %q, mau %q", addr, got, want)
		}
	}
}

func TestResolveServerAddressKeepsLiteralIP(t *testing.T) {
	got, err := resolveServerAddress("203.0.113.5", time.Second)
	if err != nil || got != "203.0.113.5" {
		t.Fatalf("IP literal harus dipakai apa adanya: %q %v", got, err)
	}
	got, err = resolveServerAddress("2001:db8::5", time.Second)
	if err != nil || got != "2001:db8::5" {
		t.Fatalf("IPv6 literal harus dipakai apa adanya: %q %v", got, err)
	}
}

func TestParseCoreVersion(t *testing.T) {
	cases := []struct {
		out    string
		minor  int
		ok     bool
		modern bool
	}{
		{"sing-box version v1.11.15\n\nEnvironment: go1.23.12 windows/amd64\n", 11, true, false},
		{"sing-box version 1.13.8\n", 13, true, true},
		{"sing-box version v1.14.4\n", 14, true, true},
		{"sing-box version 2.0.1\n", 100, true, true},
		{"bukan sing-box", 0, false, false},
	}
	for _, c := range cases {
		minor, ok := parseCoreMinor(c.out)
		if ok != c.ok || (ok && minor != c.minor) {
			t.Fatalf("%q: minor=%d ok=%v, mau %d/%v", c.out, minor, ok, c.minor, c.ok)
		}
		if got := minorModern(minor, ok); got != c.modern {
			t.Fatalf("%q: modern=%v, mau %v", c.out, got, c.modern)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func TestNormalizePingInterval(t *testing.T) {
	cases := map[int]int{
		0:  defaultPingInterval,
		1:  defaultPingInterval,
		4:  defaultPingInterval,
		5:  5,
		15: 15,
		60: 60,
	}
	for in, want := range cases {
		if got := normalizePingInterval(in); got != want {
			t.Fatalf("normalizePingInterval(%d) = %d, mau %d", in, got, want)
		}
	}
}

// Log core opsional: tanpa env ini stdout core tidak disimpan, jadi tidak ada
// bukti untuk menelusuri kejadian seperti "kenapa putus".
func TestConfigCoreLogEnv(t *testing.T) {
	p, err := parseVLESS(sampleURI)
	if err != nil {
		t.Fatal(err)
	}
	p.Address = "203.0.113.10"

	raw, err := buildSingboxConfig(p, 443, false)
	if err != nil {
		t.Fatal(err)
	}
	var quiet map[string]any
	if err := json.Unmarshal(raw, &quiet); err != nil {
		t.Fatal(err)
	}
	if dict(t, quiet["log"])["level"] != "warn" {
		t.Fatal("tanpa PANTUNNEL_CORE_LOG, level harus warn")
	}

	t.Setenv("PANTUNNEL_CORE_LOG", filepath.Join(t.TempDir(), "core.log"))
	raw, err = buildSingboxConfig(p, 443, false)
	if err != nil {
		t.Fatal(err)
	}
	var loud map[string]any
	if err := json.Unmarshal(raw, &loud); err != nil {
		t.Fatal(err)
	}
	logMap := dict(t, loud["log"])
	if logMap["level"] != "debug" || logMap["output"] == "" {
		t.Fatalf("PANTUNNEL_CORE_LOG harus menyalakan log debug ke file: %#v", logMap)
	}
}
