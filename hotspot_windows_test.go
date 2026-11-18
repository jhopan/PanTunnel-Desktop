//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The ICS helper is PowerShell source generated from a Go string constant, so a
// quoting slip would only surface on a live Hotspot. Let the PowerShell parser
// judge it instead.
func TestICSScriptParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ics.ps1")
	if err := os.WriteFile(path, []byte(icsScript), 0600); err != nil {
		t.Fatal(err)
	}
	check := "$errors = $null; " +
		"[System.Management.Automation.Language.Parser]::ParseFile('" + path + "', [ref]$null, [ref]$errors) | Out-Null; " +
		"if ($errors.Count -gt 0) { $errors | ForEach-Object { $_.Message }; exit 1 }"
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", check)
	setNoConsole(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("skrip ICS tidak valid: %v\n%s", err, out)
	}
}

// Sharing must be taken as the public side of the TUN and given back on
// disable; losing either half would leave a hotspot without internet.
func TestICSScriptSharesTunAndRestores(t *testing.T) {
	for _, want := range []string{
		"HNetCfg.HNetShare",
		"EnableSharing(0)",
		"EnableSharing(1)",
		"DisableSharing()",
		"$Tun",
		"$Private",
		"$RestorePublic",
	} {
		if !strings.Contains(icsScript, want) {
			t.Fatalf("skrip ICS kehilangan %s", want)
		}
	}
}
