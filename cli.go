package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// runCLI handles the non-GUI entry points and reports whether main should stop.
//
//	PanTunnel-Desktop --dump-config <file> [--vless <uri>] [--dialect auto|legacy|modern]
//	PanTunnel-Desktop --connect <detik>
//
// --dump-config renders the exact sing-box config the GUI would run, so the
// config can be validated (core check) on a machine without a GUI session.
// --connect runs the same start/probe path as the CONNECT button without a GUI.
func runCLI(args []string) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dump := fs.String("dump-config", "", "tulis config sing-box ke file ini, lalu keluar")
	uri := fs.String("vless", "", "URI vless:// untuk --dump-config")
	dialect := fs.String("dialect", "auto", "dialek config: auto|legacy|modern")
	connect := fs.Int("connect", 0, "sambungkan tunnel, tunggu N detik, lalu putus (diagnosa)")
	if err := fs.Parse(args); err != nil {
		return 2, true
	}
	if *connect > 0 {
		return runConnectOnce(*connect), true
	}
	if *dump == "" {
		return 0, false
	}

	var (
		p   *profile
		err error
	)
	if *uri != "" {
		parsed, perr := parseVLESS(*uri)
		if perr != nil {
			fmt.Fprintln(os.Stderr, "GAGAL:", perr)
			return 1, true
		}
		p = &parsed
	} else if cfg := loadConfig(); true {
		for i := range cfg.Profiles {
			if cfg.Profiles[i].ID == cfg.ActiveID {
				p = &cfg.Profiles[i]
				break
			}
		}
		if p == nil && len(cfg.Profiles) > 0 {
			p = &cfg.Profiles[0]
		}
	}
	if p == nil {
		fmt.Fprintln(os.Stderr, "GAGAL: tidak ada profil aktif; pakai --vless <uri>")
		return 1, true
	}
	port, err := parsePort(p.Port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "GAGAL: port tidak valid:", p.Port)
		return 1, true
	}

	modern := false
	switch *dialect {
	case "modern":
		modern = true
	case "legacy":
		modern = false
	default:
		if core, cerr := coreExecutable(); cerr == nil {
			modern = coreModern(core)
		}
	}

	// Sama seperti jalur connect: pakai IP kalau bisa diresolusi. Saat CI (URI
	// contoh yang tidak ada) hostname dibiarkan apa adanya; config tetap valid
	// untuk `sing-box check`.
	if addr, err := resolveServerAddress(p.Address, 5*time.Second); err == nil {
		p.Address = addr
	} else {
		fmt.Fprintln(os.Stderr, "catatan: hostname server tidak bisa diresolusi, memakai hostname di config")
	}

	rendered, err := buildSingboxConfig(*p, port, modern)
	if err != nil {
		fmt.Fprintln(os.Stderr, "GAGAL:", err)
		return 1, true
	}
	if err := os.WriteFile(*dump, rendered, 0644); err != nil {
		fmt.Fprintln(os.Stderr, "GAGAL:", err)
		return 1, true
	}
	fmt.Printf("config ditulis ke %s (dialek: %s)\n", *dump, map[bool]string{true: "modern", false: "legacy"}[modern])
	return 0, true
}

// runConnectOnce menjalankan jalur koneksi yang sama dengan tombol CONNECT
// (uiState.start) tanpa GUI. Berguna untuk diagnosa: status hanya menjadi
// "Connected" setelah HTTP 204 nyata lewat TUN.
func runConnectOnce(timeoutSec int) int {
	u := initUI(loadConfig())
	p := u.getActiveProfile()
	if p == nil || p.Address == "" || p.UUID == "" {
		fmt.Fprintln(os.Stderr, "GAGAL: profil aktif belum lengkap")
		return 1
	}
	fmt.Printf("profil: %s (%s:%s%s)\n", p.Name, p.Address, p.Port, p.Path)

	u.start()
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for time.Now().Before(deadline) {
		status, ping := u.statusSnapshot()
		switch {
		case status == "Connected":
			fmt.Printf("status: %s · %s\n", status, ping)
			u.stop()
			return 0
		case strings.HasPrefix(status, "GAGAL"):
			fmt.Fprintln(os.Stderr, "status:", status)
			u.stop()
			return 1
		}
		time.Sleep(500 * time.Millisecond)
	}
	status, _ := u.statusSnapshot()
	fmt.Fprintf(os.Stderr, "TIMEOUT setelah %d detik (status: %s)\n", timeoutSec, status)
	u.stop()
	return 1
}
