package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// File lisensi (.pantunnel) mengikat satu profil VLESS ke satu HWID, boleh
// dengan tanggal kedaluwarsa.
//
// Batas keamanannya penting dipahami: kunci diturunkan dari HWID, dan HWID
// ditampilkan di aplikasi, jadi ini **bukan** DRM kriptografis. Gunanya
// mencegah file dibagikan sembarangan antar perangkat dan memudahkan
// kedaluwarsa — sama seperti desain lisensi offline di aplikasi Android.
// Rincian: docs/LISENSI-HWID.md
// androidLicensePrefix: awalan file terkunci dari PanTunnel Android (LicenseCodec.kt).
// Formatnya berbeda (PNT1: + base64 biner, kunci SHA-256(hwid+salt), payload lain),
// jadi file itu tidak bisa dibuka di desktop.
const androidLicensePrefix = "PNT1:"

const (
	licenseVersion = 1
	licenseSalt    = "PanTunnel-license-v1"
	licenseDateFmt = "2006-01-02"
)

type licenseFile struct {
	Version int    `json:"v"`
	HWID    string `json:"hwid"`
	Expire  string `json:"expire,omitempty"`
	Nonce   string `json:"nonce"`
	Data    string `json:"data"`
}

type licensePayload struct {
	Name   string `json:"name"`
	URI    string `json:"uri"`
	Expire string `json:"expire,omitempty"`
	Issued string `json:"issued"`
}

// normalizeHWID membuat HWID tahan bentuk penulisan: huruf besar, tanpa spasi,
// tanpa tanda hubung. HWID ditampilkan sebagai hex, tapi orang sering menyalin
// dengan spasi atau tanda hubung.
func normalizeHWID(hwid string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(hwid)) {
		if r == ' ' || r == '\t' || r == '-' || r == '_' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// licenseKey menurunkan kunci AES-256 dari HWID.
func licenseKey(hwid string) []byte {
	sum := sha256.Sum256([]byte(licenseSalt + "|" + normalizeHWID(hwid)))
	return sum[:]
}

func licenseAAD(hwid, expire string) []byte {
	return []byte(fmt.Sprintf("%d:%s:%s", licenseVersion, normalizeHWID(hwid), expire))
}

// createLicense membungkus satu URI menjadi file lisensi untuk HWID tertentu.
// Dipakai tooling admin (`--license-create`), bukan tombol di GUI.
func createLicense(hwid, uri, name, expire string) ([]byte, error) {
	uri = strings.TrimSpace(uri)
	if _, err := parseVLESS(uri); err != nil {
		return nil, fmt.Errorf("URI tidak didukung: %w", err)
	}
	if normalizeHWID(hwid) == "" {
		return nil, fmt.Errorf("HWID kosong")
	}
	if expire != "" {
		if _, err := time.Parse(licenseDateFmt, expire); err != nil {
			return nil, fmt.Errorf("tanggal kedaluwarsa harus YYYY-MM-DD")
		}
	}
	payload := licensePayload{
		Name:   name,
		URI:    uri,
		Expire: expire,
		Issued: time.Now().Format(licenseDateFmt),
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(licenseKey(hwid))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, plain, licenseAAD(hwid, expire))

	out, err := json.MarshalIndent(licenseFile{
		Version: licenseVersion,
		HWID:    normalizeHWID(hwid),
		Expire:  expire,
		Nonce:   base64.StdEncoding.EncodeToString(nonce),
		Data:    base64.StdEncoding.EncodeToString(sealed),
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// isLicenseFile mendeteksi file lisensi tanpa membuka isinya.
func isLicenseFile(raw []byte) bool {
	var f licenseFile
	if json.Unmarshal(raw, &f) != nil {
		return false
	}
	return f.Version != 0 && f.Nonce != "" && f.Data != ""
}

// openLicense membuka file lisensi memakai HWID perangkat ini.
func openLicense(raw []byte, hwid string, now time.Time) (profile, error) {
	var f licenseFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return profile{}, fmt.Errorf("file lisensi tidak bisa dibaca")
	}
	if f.Version != licenseVersion {
		return profile{}, fmt.Errorf("versi lisensi %d tidak dikenal (aplikasi mendukung %d)", f.Version, licenseVersion)
	}
	if normalizeHWID(f.HWID) != normalizeHWID(hwid) {
		return profile{}, fmt.Errorf("lisensi ini untuk HWID %s, perangkat ini %s", f.HWID, normalizeHWID(hwid))
	}
	nonce, err := base64.StdEncoding.DecodeString(f.Nonce)
	if err != nil {
		return profile{}, fmt.Errorf("nonce lisensi rusak")
	}
	sealed, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		return profile{}, fmt.Errorf("isi lisensi rusak")
	}
	block, err := aes.NewCipher(licenseKey(hwid))
	if err != nil {
		return profile{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return profile{}, err
	}
	plain, err := gcm.Open(nil, nonce, sealed, licenseAAD(f.HWID, f.Expire))
	if err != nil {
		// Header diubah, HWID tidak cocok, atau file rusak.
		return profile{}, fmt.Errorf("lisensi tidak sah untuk perangkat ini (kunci tidak cocok)")
	}
	var payload licensePayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return profile{}, fmt.Errorf("isi lisensi tidak bisa dibaca")
	}
	if licenseExpired(payload.Expire, now) {
		return profile{}, fmt.Errorf("lisensi sudah kedaluwarsa sejak %s", payload.Expire)
	}
	p, err := parseVLESS(payload.URI)
	if err != nil {
		return profile{}, fmt.Errorf("URI di lisensi tidak didukung: %w", err)
	}
	if payload.Name != "" {
		p.Name = payload.Name
	}
	p.LicenseExpire = payload.Expire
	return p, nil
}

// licenseExpired: tanggal kedaluwarsa berlaku sampai akhir hari itu.
func licenseExpired(expire string, now time.Time) bool {
	expire = strings.TrimSpace(expire)
	if expire == "" {
		return false
	}
	limit, err := time.Parse(licenseDateFmt, expire)
	if err != nil {
		return false
	}
	return now.After(limit.AddDate(0, 0, 1))
}

// licenseSummary untuk ditampilkan di kartu/status.
func licenseSummary(expire string) string {
	if strings.TrimSpace(expire) == "" {
		return "lisensi tanpa batas waktu"
	}
	return "lisensi berlaku sampai " + expire
}
