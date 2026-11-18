//go:build !windows

package main

import (
	"fmt"
	"os"
)

func hotspotQuery() hotspotStatus {
	return hotspotStatus{Supported: hotspotPlatformSupported}
}

// hotspotPlatformSupported: ICS hanya ada di Windows.
const hotspotPlatformSupported = false

func hotspotEnable(string) (string, error) {
	return "", fmt.Errorf("hotspot share hanya tersedia di Windows")
}

func hotspotDisable(string, string) {}

func runICSCommand(string) int {
	fmt.Fprintln(os.Stderr, "GAGAL: hotspot share hanya tersedia di Windows")
	return 1
}
