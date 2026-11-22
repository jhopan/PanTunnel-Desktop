//go:build windows

package main

import (
	"os"
	"strconv"
	"testing"
)

// Nama unik per test: kalau aplikasi sedang jalan di mesin ini, nama produksi
// akan terdeteksi sebagai duplikat dan test jadi palsu.
func TestClaimInstanceDetectsDuplicate(t *testing.T) {
	name := `Local\PanTunnel-Desktop-test-` + strconv.Itoa(os.Getpid())

	if claimInstance(name) {
		t.Fatal("klaim pertama tidak boleh dianggap duplikat")
	}
	if !claimInstance(name) {
		t.Fatal("klaim kedua harus terdeteksi duplikat")
	}
	releaseInstance()
	if claimInstance(name) {
		t.Fatal("setelah dilepas, klaim berikutnya harus berhasil")
	}
	releaseInstance()
}
