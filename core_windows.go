//go:build windows

package main

// coreNames are the sing-box core binaries searched next to the app binary,
// in order. The JPS core name is kept so an existing installation keeps
// working when the user swaps the app only.
var coreNames = []string{
	"PanTunnel-Core-windows-amd64.exe",
	"JPS-Tunnel-Core-windows-amd64.exe",
	"sing-box.exe",
}
