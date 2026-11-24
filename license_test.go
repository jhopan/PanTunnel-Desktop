package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const licenseURI = "vless://44444444-4444-4444-4444-444444444444@lic.example:443?type=ws&security=tls&path=%2Fvless&sni=lic.example&host=lic.example#Lisensi"

func TestLicenseRoundTrip(t *testing.T) {
	raw, err := createLicense("AB12-CD34", licenseURI, "Pelanggan A", "2027-01-31")
	if err != nil {
		t.Fatal(err)
	}
	if !isLicenseFile(raw) {
		t.Fatal("hasil createLicense harus dikenali sebagai file lisensi")
	}

	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	// HWID dari layar Pengaturan sering disalin dengan spasi/huruf kecil.
	for _, hwid := range []string{"AB12-CD34", " ab12-cd34 ", "ab12 cd34"} {
		p, err := openLicense(raw, hwid, now)
		if err != nil {
			t.Fatalf("HWID %q harus bisa membuka lisensi: %v", hwid, err)
		}
		if p.Name != "Pelanggan A" || p.Address != "lic.example" || p.Path != "/vless" {
			t.Fatalf("profil dari lisensi salah: %#v", p)
		}
		if p.LicenseExpire != "2027-01-31" {
			t.Fatalf("kedaluwarsa tidak ikut: %q", p.LicenseExpire)
		}
	}
}

func TestLicenseRejectsOtherHWID(t *testing.T) {
	raw, err := createLicense("AB12-CD34", licenseURI, "Pelanggan A", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = openLicense(raw, "FF99-EE88", time.Now())
	if err == nil {
		t.Fatal("HWID lain tidak boleh membuka lisensi")
	}
	if !strings.Contains(err.Error(), "HWID") {
		t.Fatalf("pesan error harus menyebut HWID: %v", err)
	}
}

func TestLicenseRejectsExpired(t *testing.T) {
	raw, err := createLicense("AB12-CD34", licenseURI, "Pelanggan A", "2026-01-10")
	if err != nil {
		t.Fatal(err)
	}
	// Masih berlaku pada hari-H (termasuk akhir hari itu).
	if _, err := openLicense(raw, "AB12-CD34", time.Date(2026, 1, 10, 23, 30, 0, 0, time.UTC)); err != nil {
		t.Fatalf("tanggal kedaluwarsa sendiri masih sah: %v", err)
	}
	// Sehari setelahnya ditolak.
	_, err = openLicense(raw, "AB12-CD34", time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("lisensi kedaluwarsa harus ditolak")
	}
	if !strings.Contains(err.Error(), "kedaluwarsa") {
		t.Fatalf("pesan harus jelas soal kedaluwarsa: %v", err)
	}
}

// Header (termasuk tanggal) ikut ditandatangani lewat AAD: mengubahnya membuat
// tag GCM gagal, jadi tanggal tidak bisa diperpanjang sendiri oleh user.
func TestLicenseRejectsTamperedHeader(t *testing.T) {
	raw, err := createLicense("AB12-CD34", licenseURI, "Pelanggan A", "2026-06-30")
	if err != nil {
		t.Fatal(err)
	}
	var f licenseFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	f.Expire = "2030-06-30"
	tampered, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openLicense(tampered, "AB12-CD34", time.Now()); err == nil {
		t.Fatal("header yang diubah harus membuat lisensi tidak sah")
	}
}

func TestCreateLicenseValidation(t *testing.T) {
	if _, err := createLicense("AB12-CD34", "vless://x@h:443?type=tcp&security=tls", "", ""); err == nil {
		t.Fatal("URI non WS+TLS harus ditolak")
	}
	if _, err := createLicense("", licenseURI, "", ""); err == nil {
		t.Fatal("HWID kosong harus ditolak")
	}
	if _, err := createLicense("AB12-CD34", licenseURI, "", "31-01-2027"); err == nil {
		t.Fatal("format tanggal salah harus ditolak")
	}
}

func TestIsLicenseFileRejectsOrdinaryText(t *testing.T) {
	if isLicenseFile([]byte(licenseURI)) {
		t.Fatal("URI biasa bukan file lisensi")
	}
	if isLicenseFile([]byte(`{"profiles":[],"active_id":""}`)) {
		t.Fatal("settings.json bukan file lisensi")
	}
}

func TestLicenseExpiredBoundaries(t *testing.T) {
	day := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	if licenseExpired("", day) {
		t.Fatal("tanpa tanggal = tanpa batas")
	}
	if licenseExpired("2026-05-01", day) {
		t.Fatal("hari kedaluwarsa masih sah")
	}
	if !licenseExpired("2026-04-30", day) {
		t.Fatal("sehari setelah tanggal harus kedaluwarsa")
	}
	if licenseExpired("bukan-tanggal", day) {
		t.Fatal("tanggal rusak tidak boleh memblokir (config dianggap tanpa batas)")
	}
}

// Jalur import lengkap: file lisensi → profil di daftar → tersimpan ke settings.
// PANTUNNEL_DATA_DIR dipakai supaya test tidak menyentuh data user.
func TestImportLicenseAddsProfile(t *testing.T) {
	t.Setenv("PANTUNNEL_DATA_DIR", t.TempDir())

	const installation = "0123456789abcdef0123456789abcdef"
	hwid := makeHWID(installation)
	raw, err := createLicense(hwid, licenseURI, "Pelanggan B", "2027-03-01")
	if err != nil {
		t.Fatal(err)
	}

	u := &uiState{data: configData{InstallationID: installation}}
	u.importLicense(string(raw))

	if len(u.data.Profiles) != 1 {
		t.Fatalf("profil dari lisensi harus ditambahkan: %#v (status: %s)", u.data.Profiles, u.status)
	}
	got := u.data.Profiles[0]
	if got.Name != "Pelanggan B" || got.LicenseExpire != "2027-03-01" {
		t.Fatalf("profil salah: %#v", got)
	}
	if u.data.ActiveID != got.ID {
		t.Fatal("profil pertama harus jadi aktif kalau belum ada yang aktif")
	}
	if !strings.Contains(u.status, "Lisensi OK") {
		t.Fatalf("status harus melaporkan lisensi OK: %q", u.status)
	}

	// Import kedua kali: profil sudah ada, tidak boleh dobel.
	u.importLicense(string(raw))
	if len(u.data.Profiles) != 1 {
		t.Fatalf("import ulang tidak boleh menambah profil: %d", len(u.data.Profiles))
	}

	// Lisensi milik HWID lain harus ditolak dan tidak mengubah daftar.
	other, err := createLicense("DEADBEEF", licenseURI, "Orang Lain", "")
	if err != nil {
		t.Fatal(err)
	}
	u.importLicense(string(other))
	if len(u.data.Profiles) != 1 {
		t.Fatal("lisensi HWID lain tidak boleh menambah profil")
	}
	if !strings.HasPrefix(u.status, "GAGAL:") {
		t.Fatalf("status harus GAGAL untuk lisensi asing: %q", u.status)
	}
}
