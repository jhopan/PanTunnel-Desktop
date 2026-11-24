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

// Jalur export: profil aktif → URI → lisensi terkunci untuk HWID pembeli.
func TestBuildLicenseFromActiveProfile(t *testing.T) {
	t.Setenv("PANTUNNEL_DATA_DIR", t.TempDir())
	p, err := parseVLESS(licenseURI)
	if err != nil {
		t.Fatal(err)
	}
	p.Name = "Server Pan"
	u := &uiState{data: configData{Profiles: []profile{p}, ActiveID: p.ID}}

	// HWID harus 24 karakter.
	if _, err := u.buildLicense("ABC", "", ""); err == nil {
		t.Fatal("HWID pendek harus ditolak")
	}
	// Tanpa profil aktif yang lengkap.
	empty := &uiState{}
	if _, err := empty.buildLicense("453A6791CD2ED2731FF6E5E5", "", ""); err == nil {
		t.Fatal("tanpa profil lengkap harus ditolak")
	}

	raw, err := u.buildLicense(" 453a-6791-CD2E-D2731FF6E5E5 ", "", "2028-01-31")
	if err != nil {
		t.Fatal(err)
	}
	// Dibuka oleh HWID pembeli (bentuk apa pun) → berhasil, nama dari profil.
	buyer, err := openLicense(raw, "453A6791CD2ED2731FF6E5E5", time.Now())
	if err != nil {
		t.Fatalf("lisensi hasil export harus bisa dibuka pembeli: %v", err)
	}
	if buyer.Name != "Server Pan" || buyer.Address != "lic.example" || buyer.UUID != p.UUID {
		t.Fatalf("profil di lisensi tidak sama: %#v", buyer)
	}
	if buyer.LicenseExpire != "2028-01-31" {
		t.Fatalf("kedaluwarsa export salah: %q", buyer.LicenseExpire)
	}
	// HWID lain tetap ditolak.
	if _, err := openLicense(raw, "FFFFFFFFFFFFFFFFFFFFFFFF", time.Now()); err == nil {
		t.Fatal("HWID lain tidak boleh membuka hasil export")
	}
}

func TestProfileURIRoundTrip(t *testing.T) {
	original, err := parseVLESS(sampleURI)
	if err != nil {
		t.Fatal(err)
	}
	again, err := parseVLESS(profileURI(original))
	if err != nil {
		t.Fatalf("URI hasil susun ulang harus valid: %v", err)
	}
	if again.Address != original.Address || again.Port != original.Port || again.UUID != original.UUID ||
		again.Path != original.Path || again.SNI != original.SNI || again.Host != original.Host {
		t.Fatalf("round-trip berubah:\nawal: %#v\nulang: %#v", original, again)
	}

	v6 := original
	v6.Address = "2001:db8::1"
	v6.Port = "8443"
	parsed6, err := parseVLESS(profileURI(v6))
	if err != nil {
		t.Fatalf("IPv6 harus bisa disusun ulang: %v", err)
	}
	if parsed6.Address != "2001:db8::1" || parsed6.Port != "8443" {
		t.Fatalf("IPv6 berubah: %#v", parsed6)
	}
}

func TestFileNameForLicense(t *testing.T) {
	u := &uiState{data: configData{Profiles: []profile{{Name: "Pelanggan A/B:*?"}}, ActiveID: "x"}}
	got := u.fileNameForLicense("")
	if got != "Pelanggan-AB.pantunnel" {
		t.Fatalf("nama file harus dibersihkan: %q", got)
	}
	if name := u.fileNameForLicense("  "); name != "Pelanggan-AB.pantunnel" {
		t.Fatalf("nama kosong harus jatuh ke nama profil: %q", name)
	}
	if name := (&uiState{}).fileNameForLicense(""); name != "profil.pantunnel" {
		t.Fatalf("tanpa profil apa pun harus 'profil.pantunnel': %q", name)
	}
}

// File terkunci dari aplikasi Android memakai format lain (PNT1:…) dan tidak
// boleh menambah profil; pesannya harus jelas, bukan "tidak ada URI".
func TestImportRejectsAndroidLockedFile(t *testing.T) {
	t.Setenv("PANTUNNEL_DATA_DIR", t.TempDir())
	u := &uiState{data: configData{InstallationID: "0123456789abcdef"}}
	u.importText("test.pantunnel", "PNT1:AQECAwQFBgcICQoLDA0ODw==")
	if len(u.data.Profiles) != 0 {
		t.Fatal("file Android tidak boleh menghasilkan profil")
	}
	if !strings.Contains(u.status, "Android") || !strings.Contains(u.status, "PNT1") {
		t.Fatalf("pesan harus menjelaskan file Android: %q", u.status)
	}
}
