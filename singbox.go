package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TUN identity and addressing.
//
// The IPv6 address is not decoration: with it sing-box installs IPv6 routes on
// the TUN adapter too, so IPv6 traffic that cannot ride the tunnel is dropped
// inside the tunnel instead of escaping through the physical adapter. That is
// the fail-closed behaviour a no-leak client needs.
//
// The IPv4 subnet is deliberately off the path beaten by other sing-box
// clients (172.19.0.1/30): when two clients run at once, a duplicate TUN
// address makes Windows route traffic unpredictably.
const (
	tunInterfaceName = "pantunnel-tun"
	tunAddress4      = "172.31.19.1/30"
	tunAddress6      = "fdfe:dcba:9876::1/126"
)

// LAN destinations stay outside the tunnel: perangkat lokal, printer, router,
// dan apa pun di jaringan rumahl/kantor tetap bisa dijangkau langsung.
var lanRoutes = []string{
	"127.0.0.0/8",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
}

// parseURIList mengumpulkan semua URI vless:// dari teks apa pun (clipboard,
// file .txt/.json/.conf): URI yang tidak didukung dilewati, duplikat dibuang.
func parseURIList(text string) []profile {
	var out []profile
	seen := map[string]bool{}
	rest := text
	for {
		idx := strings.Index(rest, "vless://")
		if idx < 0 {
			return out
		}
		rest = rest[idx:]
		end := strings.IndexAny(rest, " \t\r\n\"',;")
		raw := rest
		if end >= 0 {
			raw = rest[:end]
		}
		rest = rest[len(raw):]

		p, err := parseVLESS(raw)
		if err != nil {
			continue
		}
		key := profileKey(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
}

// profileKey dipakai untuk membuang duplikat saat import.
func profileKey(p profile) string {
	return p.Address + "|" + p.Port + "|" + p.UUID + "|" + p.Path + "|" + p.SNI + "|" + p.Host
}

// coreVersionRe matches the banner printed by `sing-box version`.
var coreVersionRe = regexp.MustCompile(`v?(\d+)\.(\d+)\.`)

func parseVLESS(raw string) (profile, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "vless" || u.User == nil || u.Hostname() == "" {
		return profile{}, fmt.Errorf("bukan URI vless:// yang valid")
	}
	q := u.Query()
	if !strings.EqualFold(q.Get("type"), "ws") || !strings.EqualFold(q.Get("security"), "tls") {
		return profile{}, fmt.Errorf("hanya transport ws + TLS yang didukung")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	if _, err := parsePort(port); err != nil {
		return profile{}, err
	}
	sni := q.Get("sni")
	if sni == "" {
		sni = u.Hostname()
	}
	host := q.Get("host")
	if host == "" {
		host = sni
	}
	path := q.Get("path")
	if path == "" {
		path = "/"
	}
	name := u.Fragment
	if name == "" {
		name = u.Hostname()
	}
	return profile{
		Name:    name,
		Address: u.Hostname(),
		Port:    port,
		UUID:    u.User.Username(),
		Path:    path,
		SNI:     sni,
		Host:    host,
	}, nil
}

// coreExecutable resolves the sing-box core that ships next to the app binary.
// PANTUNNEL_CORE overrides the search and is also the hook used by tests/CI.
func coreExecutable() (string, error) {
	if override := strings.TrimSpace(os.Getenv("PANTUNNEL_CORE")); override != "" {
		info, err := os.Stat(override)
		if err != nil || info.Size() < 1_000_000 {
			return "", fmt.Errorf("PANTUNNEL_CORE tidak valid: %s", override)
		}
		return override, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	var tried []string
	for _, name := range coreNames {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && info.Size() >= 1_000_000 {
			return p, nil
		}
		tried = append(tried, name)
	}
	return "", fmt.Errorf("core tidak ditemukan di %s (cari: %s)", dir, strings.Join(tried, ", "))
}

// coreModern reports whether the core speaks the sing-box >= 1.12 config
// dialect (new DNS server format, domain_resolver instead of domain_strategy,
// no independent_cache). Anything we cannot identify is treated as legacy,
// because the bundled core (JPS core-v1.0.2 = sing-box 1.11.x) is legacy.
func coreModern(corePath string) bool {
	cmd := exec.Command(corePath, "version")
	setNoConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return minorModern(parseCoreMinor(string(out)))
}

// parseCoreMinor reads the minor version out of the `sing-box version` banner.
func parseCoreMinor(output string) (int, bool) {
	m := coreVersionRe.FindStringSubmatch(output)
	if m == nil {
		return 0, false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	minor, err := strconv.Atoi(m[2])
	if err != nil {
		return 0, false
	}
	if major > 1 {
		return 100, true
	}
	return minor, true
}

// minorModern answers whether a detected minor version uses the new dialect.
func minorModern(minor int, ok bool) bool {
	return ok && minor >= 12
}

// hostRoute converts a server address into a single-host route (/32 or /128).
func hostRoute(addr string) string {
	ip := net.ParseIP(addr)
	if ip == nil {
		return ""
	}
	if ip.To4() != nil {
		return addr + "/32"
	}
	return addr + "/128"
}

// resolveServerAddress pins the VPN server hostname to an IP *before* the TUN
// comes up. Once the TUN is up the system resolver points at the tunnel itself,
// so a hostname here would be a bootstrap deadlock: the tunnel needs the
// server, the server name needs the tunnel.
func resolveServerAddress(host string, timeout time.Duration) (string, error) {
	if net.ParseIP(host) != nil {
		return host, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip.String(), nil
		}
	}
	if len(ips) > 0 {
		return ips[0].String(), nil
	}
	return "", fmt.Errorf("tidak ada alamat IP untuk %s", host)
}

// buildSingboxConfig renders the sing-box config for one VLESS profile.
//
// Anti-leak shape, in order of importance:
//   - strict_route: Windows gets WFP filters that stop the ordinary multihomed
//     DNS behaviour from answering over the physical adapter.
//   - hijack-dns route rule: every DNS packet that enters the TUN is answered
//     by the tunnel DNS servers, whatever address the client asked.
//   - every configured resolver sits inside the tunnel. There is no local
//     resolver at all: p.Address must already be an IP, resolved *before* the
//     TUN comes up (see resolveServerAddress). A hostname here would deadlock,
//     because once the TUN is up the system resolver points at the tunnel.
//   - LAN ranges and the VPN server itself are excluded from the tunnel routes.
func buildSingboxConfig(p profile, port int, modern bool) ([]byte, error) {
	appDir := appDataDir()
	serverAddr := p.Address

	dnsServers := []any{}
	if modern {
		dnsServers = append(dnsServers,
			map[string]any{"tag": "dns-tunnel", "type": "udp", "server": "1.1.1.1", "detour": "proxy"},
			map[string]any{"tag": "dns-backup", "type": "udp", "server": "8.8.8.8", "detour": "proxy"},
		)
	} else {
		dnsServers = append(dnsServers,
			map[string]any{"tag": "dns-tunnel", "address": "1.1.1.1", "strategy": "prefer_ipv4", "detour": "proxy"},
			map[string]any{"tag": "dns-backup", "address": "8.8.8.8", "strategy": "prefer_ipv4", "detour": "proxy"},
		)
	}

	dns := map[string]any{
		"servers": dnsServers,
		"final":   "dns-tunnel",
	}
	if !modern {
		dns["strategy"] = "prefer_ipv4"
		dns["independent_cache"] = true
	}

	proxyOutbound := map[string]any{
		"type":        "vless",
		"tag":         "proxy",
		"server":      serverAddr,
		"server_port": port,
		"uuid":        p.UUID,
		// No hostname verification: these deployments terminate TLS with a
		// fronted certificate, so the chain never matches the SNI.
		"tls": map[string]any{
			"enabled":     true,
			"server_name": p.SNI,
			"insecure":    true,
		},
		"transport": map[string]any{
			"type":    "ws",
			"path":    p.Path,
			"headers": map[string]string{"Host": p.Host},
		},
	}
	if !modern {
		proxyOutbound["domain_strategy"] = "prefer_ipv4"
	}

	// Anti-loop: the VPN server must be reachable without the tunnel, both as a
	// route (the outbound's own socket) and as a route rule.
	excluded := append([]string{}, lanRoutes...)
	rules := []any{
		map[string]any{"action": "sniff"},
		// Answers LAN and tunnel DNS alike, so nothing can fall back to
		// a resolver outside the tunnel.
		map[string]any{"action": "hijack-dns", "protocol": "dns"},
	}
	if serverRoute := hostRoute(serverAddr); serverRoute != "" {
		excluded = append(excluded, serverRoute)
		rules = append(rules, map[string]any{"ip_cidr": []string{serverRoute}, "outbound": "direct"})
	}
	rules = append(rules, map[string]any{"ip_cidr": lanRoutes, "outbound": "direct"})

	routeMap := map[string]any{
		"auto_detect_interface": true,
		"rules":                 rules,
		"final":                 "proxy",
	}
	if modern {
		// Wajib sejak 1.12; hanya dipakai kalau ada dial yang memakai nama.
		routeMap["default_domain_resolver"] = map[string]any{"server": "dns-tunnel", "strategy": "prefer_ipv4"}
	}

	// Log core opsional: PANTUNNEL_CORE_LOG=/path/file.log menyalakan log debug
	// ke file. Tanpa ini stdout core tidak disimpan, jadi tidak ada bukti untuk
	// menelusuri kejadian seperti "kenapa putus".
	logMap := map[string]any{"level": "warn"}
	if logPath := strings.TrimSpace(os.Getenv("PANTUNNEL_CORE_LOG")); logPath != "" {
		logMap["level"] = "debug"
		logMap["output"] = logPath
	}

	cfg := map[string]any{
		"log": logMap,
		"dns": dns,
		"inbounds": []any{
			map[string]any{
				"type":                  "tun",
				"tag":                   "tun-in",
				"interface_name":        tunInterfaceName,
				"address":               []string{tunAddress4, tunAddress6},
				"auto_route":            true,
				"strict_route":          true,
				"stack":                 "gvisor",
				"mtu":                   1400,
				"route_exclude_address": excluded,
			},
		},
		"outbounds": []any{
			proxyOutbound,
			map[string]any{"type": "direct", "tag": "direct"},
		},
		"route": routeMap,
		"experimental": map[string]any{
			"cache_file": map[string]any{
				"enabled": true,
				"path":    filepath.Join(appDir, "cache.db"),
			},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}
