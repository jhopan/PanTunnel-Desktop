//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// instanceLock ditahan sampai proses berakhir; kernel melepas flock-nya.
var instanceLock *os.File

// anotherInstance mengunci file lock di folder data aplikasi. Instance kedua
// mendapat kegagalan lock dan tidak membuat TUN kedua.
func anotherInstance() bool {
	if err := os.MkdirAll(appDataDir(), 0700); err != nil {
		return false
	}
	f, err := os.OpenFile(filepath.Join(appDataDir(), "instance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return true
	}
	instanceLock = f
	return false
}

// focusExistingWindow tidak ada di platform ini: instance kedua cukup keluar.
func focusExistingWindow() {}
