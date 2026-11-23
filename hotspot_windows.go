//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// hotspotPlatformSupported tells the UI whether ICS exists at all, without
// paying for a helper call.
const hotspotPlatformSupported = true

// icsScript drives Windows Internet Connection Sharing (ICS) through the
// HNetCfg COM objects. ICS is the only stock Windows mechanism that gives a
// hotspot client DHCP + NAT, so sharing the TUN adapter as the "public"
// connection is what makes hotspot clients egress through the VPN.
//
// The script prints a flat KEY=value report; parseHotspotQuery reads it.
const icsScript = `param(
    [Parameter(Mandatory = $true)][ValidateSet('query', 'enable', 'disable')][string]$Action,
    [string]$Tun = 'pantunnel-tun',
    [string]$Private = '',
    [string]$RestorePublic = '',
    [string]$Subnet = '192.168.137.'
)

$ErrorActionPreference = 'Stop'
$sharing = New-Object -ComObject HNetCfg.HNetShare

function Get-ConnectionByName([string]$name) {
    foreach ($conn in $sharing.EnumEveryConnection) {
        if ([string]$sharing.NetConnectionProps($conn).Name -eq $name) { return $conn }
    }
    return $null
}

function RestorePublic([string[]]$names) {
    foreach ($name in $names) {
        $conn = Get-ConnectionByName $name
        if ($conn) {
            $cfg = $sharing.INetSharingConfigurationForINetConnection($conn)
            if (-not $cfg.SharingEnabled) { $cfg.EnableSharing(0) }
        }
    }
}

function Report {
    foreach ($conn in $sharing.EnumEveryConnection) {
        $cfg = $sharing.INetSharingConfigurationForINetConnection($conn)
        $name = [string]$sharing.NetConnectionProps($conn).Name
        if ($name -eq $Tun) { Write-Output 'TUN_PRESENT=1' }
        if ($cfg.SharingEnabled -and $cfg.SharingConnectionType -eq 0) { Write-Output ('PUBLIC=' + $name) }
        if ($cfg.SharingEnabled -and $cfg.SharingConnectionType -eq 1) { Write-Output ('PRIVATE=' + $name) }
    }
    $wfd = @(Get-NetAdapter -IncludeHidden -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceDescription -like '*Wi-Fi Direct*' })
    foreach ($adapter in $wfd) { Write-Output ('HOTSPOT_ADAPTER=' + $adapter.Name) }
    if ($wfd | Where-Object { $_.Status -eq 'Up' }) { Write-Output 'HOTSPOT_UP=1' }
    # Permanent = entri statis milik SoftAP/SSDP, bukan perangkat klien.
    $clients = @(Get-NetNeighbor -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object {
        $_.IPAddress -like ($Subnet + '*') -and
        $_.IPAddress -notlike '*.255' -and
        $_.State -ne 'Unreachable' -and
        $_.State -ne 'Permanent'
    })
    Write-Output ('CLIENTS=' + $clients.Count)
}

try {
    if ($Action -eq 'enable') {
        # ICS hanya menerima satu koneksi public. Kita ambil alih dari pemegang
        # sekarang, tapi namanya dicatat dulu: kalau ICS menolak TUN sebagai
        # public, sharing lama dikembalikan supaya hotspot tidak kehilangan
        # internet karena percobaan ini.
        $previousPublic = @()
        foreach ($conn in $sharing.EnumEveryConnection) {
            $cfg = $sharing.INetSharingConfigurationForINetConnection($conn)
            $name = [string]$sharing.NetConnectionProps($conn).Name
            if ($cfg.SharingEnabled -and $cfg.SharingConnectionType -eq 0 -and $name -ne $Tun) {
                $previousPublic += $name
                $cfg.DisableSharing()
            }
        }
        $tunConn = Get-ConnectionByName $Tun
        if (-not $tunConn) {
            RestorePublic $previousPublic
            Write-Output ('ERROR=adapter ' + $Tun + ' tidak terdaftar di ICS')
            Write-Output 'ROLLBACK=1'
            Report
            exit 2
        }
        $tunCfg = $sharing.INetSharingConfigurationForINetConnection($tunConn)
        if (-not ($tunCfg.SharingEnabled -and $tunCfg.SharingConnectionType -eq 0)) {
            try {
                $tunCfg.EnableSharing(0)
            } catch {
                RestorePublic $previousPublic
                Write-Output ('ERROR=ICS menolak ' + $Tun + ' sebagai koneksi public: ' + $_.Exception.Message)
                Write-Output 'ROLLBACK=1'
                Report
                exit 3
            }
        }
        if ($Private -ne '') {
            $privConn = Get-ConnectionByName $Private
            if ($privConn) {
                $privCfg = $sharing.INetSharingConfigurationForINetConnection($privConn)
                if (-not ($privCfg.SharingEnabled -and $privCfg.SharingConnectionType -eq 1)) {
                    # Sisi private hanya pelengkap: kalau hotspot/WFD menolak,
                    # public di TUN tidak perlu dibatalkan.
                    try { $privCfg.EnableSharing(1) } catch { Write-Output ('WARN=sisi private ' + $Private + ' gagal: ' + $_.Exception.Message) }
                }
            }
        }
    } elseif ($Action -eq 'disable') {
        $tunConn = Get-ConnectionByName $Tun
        if ($tunConn) {
            $tunCfg = $sharing.INetSharingConfigurationForINetConnection($tunConn)
            if ($tunCfg.SharingEnabled) { $tunCfg.DisableSharing() }
        }
        if ($RestorePublic -ne '') {
            $prev = Get-ConnectionByName $RestorePublic
            if ($prev) {
                $prevCfg = $sharing.INetSharingConfigurationForINetConnection($prev)
                if (-not $prevCfg.SharingEnabled) { $prevCfg.EnableSharing(0) }
            }
        }
    }
    Report
} catch {
    Write-Output ('ERROR=' + $_.Exception.Message)
    Report
}
`

func icsScriptPath() string {
	return filepath.Join(appDataDir(), "ics.ps1")
}

func runICS(args ...string) (string, error) {
	path := icsScriptPath()
	if err := os.WriteFile(path, []byte(icsScript), 0600); err != nil {
		return "", err
	}
	cmd := exec.Command("powershell.exe", append([]string{
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path,
	}, args...)...)
	setNoConsole(cmd)
	out, err := cmd.Output()
	return string(out), err
}

func hotspotQuery() hotspotStatus {
	out, err := runICS("-Action", "query", "-Tun", tunInterfaceName, "-Subnet", hotspotPrefix)
	st := parseHotspotQuery(out)
	if err != nil && strings.TrimSpace(out) == "" {
		st.Note = "helper ICS gagal: " + err.Error()
	}
	return st
}

// hotspotEnable makes the TUN adapter the shared connection and returns the
// connection that held sharing before, so it can be restored on disconnect.
func hotspotEnable(restoreHint string) (string, error) {
	state := hotspotQuery()
	if state.Shared {
		return restoreHint, nil
	}
	before := hotspotPublicConnection()
	out, err := runICS("-Action", "enable", "-Tun", tunInterfaceName, "-Private", state.Adapter)
	res := parseHotspotQuery(out)
	if res.Note != "" {
		if state.Active {
			// Windows 11 Mobile Hotspot memegang sharing-nya sendiri; ICS menolak
			// diambil alih (HRESULT 0x80040201) dan daftar sumbernya cuma berisi
			// adapter fisik. Itu batas platform, bukan bug config.
			return before, &hotspotError{"Mobile Hotspot Windows 11 memegang koneksi sharing sendiri dan menolak adapter VPN: " +
				res.Note + " — hotspot Wi-Fi tidak bisa dibagikan lewat VPN di build ini."}
		}
		return before, errText(res.Note, err)
	}
	if !res.Shared {
		return before, errText("tunnel belum menjadi koneksi publik di ICS", err)
	}
	return before, nil
}

func hotspotDisable(restorePublic string) {
	_, _ = runICS("-Action", "disable", "-Tun", tunInterfaceName, "-RestorePublic", restorePublic)
}

// hotspotPublicConnection reports which connection currently shares internet.
func hotspotPublicConnection() string {
	out, err := runICS("-Action", "query", "-Tun", tunInterfaceName, "-Subnet", hotspotPrefix)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if name, ok := strings.CutPrefix(line, "PUBLIC="); ok && name != tunInterfaceName {
			return name
		}
	}
	return ""
}

func errText(msg string, err error) error {
	if msg != "" {
		return &hotspotError{msg}
	}
	if err != nil {
		return err
	}
	return &hotspotError{"ICS tidak mengaktifkan sharing"}
}

// runICSCommand exposes the ICS helper for diagnostics and scripted setup:
//
//	PanTunnel-Desktop --ics query|enable|disable
//
// The connection whose sharing was taken over is printed as RESTORE=<name>
// and, for disable, must be given back through PANTUNNEL_RESTORE.
func runICSCommand(action string) int {
	report := func(err error) int {
		out, qerr := runICS("-Action", "query", "-Tun", tunInterfaceName, "-Subnet", hotspotPrefix)
		fmt.Print(out)
		if err != nil || qerr != nil {
			fmt.Fprintln(os.Stderr, "GAGAL:", firstError(err, qerr))
			return 1
		}
		return 0
	}
	switch action {
	case "query":
		return report(nil)
	case "enable":
		previous, err := hotspotEnable("")
		if previous != "" {
			fmt.Println("RESTORE=" + previous)
		}
		return report(err)
	case "disable":
		hotspotDisable(os.Getenv("PANTUNNEL_RESTORE"))
		return report(nil)
	}
	fmt.Fprintln(os.Stderr, "GAGAL: --ics harus query, enable, atau disable")
	return 2
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

type hotspotError struct{ msg string }

func (e *hotspotError) Error() string { return e.msg }
