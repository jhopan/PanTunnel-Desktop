package main

import (
	"encoding/json"
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
	licenseOut := fs.String("license-create", "", "tooling admin: tulis file lisensi .pantunnel ke path ini")
	licenseHWID := fs.String("hwid", "", "HWID pemilik lisensi (untuk --license-create; fallback env PANTUNNEL_HWID)")
	licenseName := fs.String("name", "", "nama profil di dalam lisensi")
	licenseExpire := fs.String("expire", "", "kedaluwarsa lisensi YYYY-MM-DD (kosong = tanpa batas)")
	licenseInfo := fs.String("license-info", "", "tooling admin: periksa file lisensi .pantunnel (read-only)")
	if err := fs.Parse(args); err != nil {
		return 2, true
	}
	if *licenseOut != "" {
		hwid := *licenseHWID
		if strings.TrimSpace(hwid) == "" {
			hwid = os.Getenv("PANTUNNEL_HWID")
		}
		return runLicenseCreate(*licenseOut, hwid, *uri, *licenseName, *licenseExpire), true
	}
	if *licenseInfo != "" {
		hwid := *licenseHWID
		if strings.TrimSpace(hwid) == "" {
			hwid = makeHWID(loadConfig().InstallationID)
		}
		return runLicenseInfo(*licenseInfo, hwid), true
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

// runLicenseCreate menulis file lisensi .pantunnel untuk satu HWID. Ini tooling
// admin (dipakai di terminal), bukan tombol di GUI: user tidak bisa mengekspor.
func runLicenseCreate(out, hwid, uri, name, expire string) int {
	if strings.TrimSpace(hwid) == "" {
		fmt.Fprintln(os.Stderr, "GAGAL: --license-create butuh --hwid <HWID pelanggan> (atau env PANTUNNEL_HWID)")
		return 1
	}
	if strings.TrimSpace(uri) == "" {
		fmt.Fprintln(os.Stderr, "GAGAL: --license-create butuh --vless <uri>")
		return 1
	}
	raw, err := createLicense(hwid, uri, name, expire)
	if err != nil {
		fmt.Fprintln(os.Stderr, "GAGAL:", err)
		return 1
	}
	if err := os.WriteFile(out, raw, 0644); err != nil {
		fmt.Fprintln(os.Stderr, "GAGAL:", err)
		return 1
	}
	fmt.Printf("lisensi ditulis ke %s\n  HWID   : %s\n  berlaku: %s\n", out, strings.ToUpper(strings.TrimSpace(hwid)), licenseSummary(expire))
	return 0
}

// runLicenseInfo memeriksa file lisensi tanpa mengubah apa pun: berguna untuk
// admin sebelum file dikirim, dan untuk mengecek HWID mana yang cocok.
func runLicenseInfo(path, hwid string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "GAGAL:", err)
		return 1
	}
	var f licenseFile
	if err := json.Unmarshal(raw, &f); err != nil {
		fmt.Fprintln(os.Stderr, "GAGAL: bukan file lisensi")
		return 1
	}
	fmt.Printf("file   : %s\nversi  : %d\npemilik: %s\nberlaku: %s\n", path, f.Version, f.HWID, licenseSummary(f.Expire))
	p, err := openLicense(raw, hwid, time.Now())
	if err != nil {
		fmt.Printf("cek    : DITOLAK untuk HWID %s — %v\n", normalizeHWID(hwid), err)
		return 1
	}
	fmt.Printf("cek    : SAH untuk HWID %s — profil %q (%s:%s%s)\n", normalizeHWID(hwid), p.Name, p.Address, p.Port, p.Path)
	return 0
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
