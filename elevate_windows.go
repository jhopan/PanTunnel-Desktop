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

// elevatedNow reports whether this process already has administrator rights.
func elevatedNow() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// ensureElevated relaunches the app through the UAC prompt when it is not an
// administrator: TUN creation and ICS need admin, and a silent failure there
// would look like a broken VPN. It reports whether this process should keep
// running: after a successful relaunch the caller must exit.
func ensureElevated() bool {
	if elevatedNow() {
		return true
	}
	// Lepas klaim single-instance sebelum relaunch: kalau tidak, proses
	// elevated akan melihat mutex ini dan keluar tanpa menampilkan window.
	releaseInstance()
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
	if ret > 32 {
		// Instance elevated sudah jalan: proses ini harus berhenti, kalau tidak
		// user melihat dua window (yang satu tanpa hak admin).
		return false
	}
	// UAC ditolak: lanjut tanpa admin. Profil tetap bisa diatur; CONNECT akan
	// gagal dengan pesan dari core.
	return true
}
