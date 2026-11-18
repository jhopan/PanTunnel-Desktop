//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32           = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
)

// ensureElevated relaunches the app through the UAC prompt when it is not an
// administrator: TUN creation and ICS need admin, and a silent failure there
// would look like a broken VPN.
func ensureElevated() bool {
	if windows.GetCurrentProcessToken().IsElevated() {
		return true
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	verb, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return false
	}
	file, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return false
	}
	// Arguments are joined verbatim: this app only ever relaunches itself with
	// a single path-less flag pair, so no quoting layer is needed.
	args, err := syscall.UTF16PtrFromString(strings.Join(os.Args[1:], " "))
	if err != nil {
		return false
	}
	dir, err := syscall.UTF16PtrFromString(filepath.Dir(exe))
	if err != nil {
		return false
	}
	ret, _, _ := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(args)),
		uintptr(unsafe.Pointer(dir)),
		1, // SW_SHOWNORMAL
	)
	// > 32 means the elevated instance was started.
	return ret > 32
}
