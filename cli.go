package main

import (
	"flag"
	"fmt"
	"os"
)

// runCLI handles the non-GUI entry points and reports whether main should stop.
//
//	PanTunnel-Desktop --dump-config <file> [--vless <uri>] [--dialect auto|legacy|modern]
//
// --dump-config renders the exact sing-box config the GUI would run, so the
// config can be validated (core check) on a machine without a GUI session.
func runCLI(args []string) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dump := fs.String("dump-config", "", "tulis config sing-box ke file ini, lalu keluar")
	uri := fs.String("vless", "", "URI vless:// untuk --dump-config")
	dialect := fs.String("dialect", "auto", "dialek config: auto|legacy|modern")
	ics := fs.String("ics", "", "hotspot share (Windows): query|enable|disable")
	if err := fs.Parse(args); err != nil {
		return 2, true
	}
	if *ics != "" {
		return runICSCommand(*ics), true
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
