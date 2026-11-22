//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// Satu instance saja: dua proses berarti dua adapter TUN, dua set route, dan
// dua binding ICS yang saling menabrak.
var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW        = kernel32.NewProc("CreateMutexW")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")

	instanceMutex syscall.Handle
)

const swRestore = 9

// anotherInstance mengambil mutex bernama untuk aplikasi ini. Handle-nya
// sengaja disimpan sampai proses berakhir: Windows yang melepasnya.
func anotherInstance() bool {
	return claimInstance(`Local\` + appName + `-single-instance`)
}

func claimInstance(name string) bool {
	ptr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	handle, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(ptr)))
	if handle == 0 {
		// Tidak bisa memastikan: lebih baik mengizinkan start daripada memblokir.
		return false
	}
	if callErr == syscall.Errno(183) { // ERROR_ALREADY_EXISTS
		syscall.CloseHandle(syscall.Handle(handle))
		return true
	}
	instanceMutex = syscall.Handle(handle)
	return false
}

// releaseInstance melepas mutex supaya proses hasil relaunch elevated bisa
// mengambilnya; tanpa ini instance elevated akan menganggap dirinya duplikat.
func releaseInstance() {
	if instanceMutex != 0 {
		syscall.CloseHandle(instanceMutex)
		instanceMutex = 0
	}
}

// focusExistingWindow membawa window instance yang sudah jalan ke depan.
// Best-effort: kalau Windows menolak, instance kedua tetap keluar diam-diam.
func focusExistingWindow() {
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		buf := make([]uint16, 512)
		n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n == 0 || syscall.UTF16ToString(buf[:n]) != appName {
			return 1
		}
		procShowWindow.Call(hwnd, swRestore)
		procSetForegroundWindow.Call(hwnd)
		return 0
	})
	procEnumWindows.Call(cb, 0)
}
