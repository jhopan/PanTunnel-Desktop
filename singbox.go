package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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

// LAN destinations stay outside the tunnel. 192.168.0.0/16 covers the Windows
// Mobile Hotspot subnet (192.168.137.0/24), so the ICS DHCP server and the
// hotspot gateway address keep working while clients still egress through
// the VPN via NAT.
var lanRoutes = []string{
	"127.0.0.0/8",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"169.254.0.0/16",
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

// buildSingboxConfig renders the sing-box config for one VLESS profile.
//
// Anti-leak shape, in order of importance:
//   - strict_route: Windows gets WFP filters that stop the ordinary multihomed
//     DNS behaviour from answering over the physical adapter.
//   - hijack-dns route rule: every DNS packet that enters the TUN is answered
//     by the tunnel DNS servers, whatever address the client asked.
//   - the only configured resolvers are inside the tunnel; the local resolver
//     exists solely to bootstrap the VPN server's own hostname.
//   - LAN ranges are excluded from the tunnel (see lanRoutes).
func buildSingboxConfig(p profile, port int, modern bool) ([]byte, error) {
	appDir := appDataDir()
	serverDomain := p.Address

	dnsServers := []any{}
	if modern {
		dnsServers = append(dnsServers,
			map[string]any{"tag": "dns-tunnel", "type": "udp", "server": "1.1.1.1", "detour": "proxy"},
			map[string]any{"tag": "dns-backup", "type": "udp", "server": "8.8.8.8", "detour": "proxy"},
			map[string]any{"tag": "dns-local", "type": "local"},
		)
	} else {
		dnsServers = append(dnsServers,
			map[string]any{"tag": "dns-tunnel", "address": "1.1.1.1", "strategy": "prefer_ipv4", "detour": "proxy"},
			map[string]any{"tag": "dns-backup", "address": "8.8.8.8", "strategy": "prefer_ipv4", "detour": "proxy"},
			map[string]any{"tag": "dns-local", "address": "local"},
		)
	}

	dns := map[string]any{
		"servers": dnsServers,
		"rules": []any{
			// The VPN server hostname must resolve before the tunnel exists.
			map[string]any{"domain": []string{serverDomain}, "server": "dns-local"},
		},
		"final": "dns-tunnel",
	}
	if !modern {
		dns["strategy"] = "prefer_ipv4"
		dns["independent_cache"] = true
	}

	proxyOutbound := map[string]any{
		"type":        "vless",
		"tag":         "proxy",
		"server":      p.Address,
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
	if modern {
		// Replaces outbound.domain_strategy (removed in sing-box 1.14) and is
		// required for every outbound that dials a hostname.
		proxyOutbound["domain_resolver"] = map[string]any{"server": "dns-local", "strategy": "prefer_ipv4"}
	} else {
		proxyOutbound["domain_strategy"] = "prefer_ipv4"
	}

	routeMap := map[string]any{
		"auto_detect_interface": true,
		"rules": []any{
			map[string]any{"action": "sniff"},
			// Answers LAN and tunnel DNS alike, so nothing can fall back to
			// a resolver outside the tunnel.
			map[string]any{"action": "hijack-dns", "protocol": "dns"},
			// Anti-loop: the VPN server is reached without the tunnel.
			map[string]any{"domain": []string{serverDomain}, "outbound": "direct"},
			map[string]any{"ip_cidr": lanRoutes, "outbound": "direct"},
		},
		"final": "proxy",
	}
	if modern {
		routeMap["default_domain_resolver"] = map[string]any{"server": "dns-local", "strategy": "prefer_ipv4"}
	}

	cfg := map[string]any{
		"log": map[string]any{"level": "warn"},
		"dns": dns,
		"inbounds": []any{
			map[string]any{
				"type":                 "tun",
				"tag":                  "tun-in",
				"interface_name":       tunInterfaceName,
				"address":              []string{tunAddress4, tunAddress6},
				"auto_route":           true,
				"strict_route":         true,
				"stack":                "gvisor",
				"mtu":                  1400,
				"route_exclude_address": lanRoutes,
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
