package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const sampleURI = "vless://00000000-0000-0000-0000-000000000000@server.example:443?type=ws&security=tls&path=%2Fvless&sni=sni.example&host=host.example#Test"

func decodeConfig(t *testing.T, modern bool) map[string]any {
	t.Helper()
	p, err := parseVLESS(sampleURI)
	if err != nil {
		t.Fatal(err)
	}
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
	for _, want := range []string{"192.168.0.0/16", "10.0.0.0/8"} {
		if !contains(excluded, want) {
			t.Fatalf("route_exclude_address harus memuat %s (LAN + subnet hotspot): %v", want, excluded)
		}
	}

	// DNS: every resolver except the bootstrap one must sit inside the tunnel.
	dns := dict(t, cfg["dns"])
	if dns["final"] != "dns-tunnel" {
		t.Fatalf("resolver final harus lewat tunnel, dapat %v", dns["final"])
	}
	for _, raw := range list(t, dns["servers"]) {
		server := dict(t, raw)
		tag := server["tag"]
		if tag == "dns-local" {
			continue
		}
		if server["detour"] != "proxy" {
			t.Fatalf("resolver %v tidak lewat tunnel: %#v", tag, server)
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
			if domains := rule["domain"]; domains != nil && contains(stringsOf(t, domains), "server.example") {
				serverDirect = true
			}
		}
	}
	if !hasHijack {
		t.Fatal("tanpa aturan hijack-dns, DNS bisa keluar TUN")
	}
	if !serverDirect {
		t.Fatal("hostname server VPN harus direct supaya tidak jadi routing loop")
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
	if dict(t, routeModern["default_domain_resolver"])["server"] != "dns-local" {
		t.Fatal("dialek modern butuh route.default_domain_resolver")
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

func TestParseHotspotQuery(t *testing.T) {
	out := "TUN_PRESENT=1\r\nPUBLIC=pantunnel-tun\r\nPUBLIC=Ethernet 2\r\nHOTSPOT_ADAPTER=Local Area Connection* 1\r\nHOTSPOT_UP=1\r\nCLIENTS=3\r\n"
	st := parseHotspotQuery(out)
	if !st.Supported || !st.Active || !st.Shared || st.Clients != 3 {
		t.Fatalf("state salah: %#v", st)
	}
	if st.Adapter != "Local Area Connection* 1" {
		t.Fatalf("adapter hotspot salah: %q", st.Adapter)
	}
	if !strings.Contains(st.summary(), "3 klien lewat VPN") {
		t.Fatal("summary tidak menyebut klien")
	}

	idle := parseHotspotQuery("TUN_PRESENT=0\nCLIENTS=0\n")
	if idle.Shared || idle.Active || idle.Note == "" {
		t.Fatalf("state idle salah: %#v", idle)
	}

	failed := parseHotspotQuery("ERROR=ICS tidak bisa dipakai\n")
	if failed.Note != "ICS tidak bisa dipakai" {
		t.Fatalf("pesan error hilang: %#v", failed)
	}
	if strings.Contains(failed.summary(), "klien lewat VPN") {
		t.Fatalf("summary mengklaim klien lewat VPN saat error: %q", failed.summary())
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
