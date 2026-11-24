# Verifikasi

Bukti yang sudah ada di repo ini, dan yang **belum** dikerjakan. Ditulis apa adanya: yang belum diuji
disebut belum diuji.

## 1. Test unit — `go test ./...`

```
--- PASS: TestLicenseRoundTrip
--- PASS: TestLicenseRejectsOtherHWID
--- PASS: TestLicenseRejectsExpired
--- PASS: TestLicenseRejectsTamperedHeader
--- PASS: TestCreateLicenseValidation
--- PASS: TestIsLicenseFileRejectsOrdinaryText
--- PASS: TestLicenseExpiredBoundaries
--- PASS: TestImportLicenseAddsProfile
--- PASS: TestParseVLESSWebSocketTLS
--- PASS: TestRejectNonWebSocketTLS
--- PASS: TestParsePort
--- PASS: TestConfigLeakProtection
--- PASS: TestConfigDialects
--- PASS: TestHostRoute
--- PASS: TestResolveServerAddressKeepsLiteralIP
--- PASS: TestParseCoreVersion
--- PASS: TestNormalizePingInterval
--- PASS: TestConfigCoreLogEnv
--- PASS: TestParseURIList
--- PASS: TestHasProfileDetectsDuplicate
--- PASS: TestClaimInstanceDetectsDuplicate
ok  github.com/jhopan/PanTunnel-Desktop
```

`go vet ./...` bersih.

## 2. Config terhadap core asli — `--dump-config` + `sing-box check`

Config yang dihasilkan aplikasi divalidasi oleh core sungguhan. Tidak menyentuh jaringan, tidak butuh
admin, aman dijalankan kapan saja.

| Core | Dialek terdeteksi | `sing-box check` |
|---|---|---|
| JPS core `core-v1.0.2` = sing-box **1.11.15** (`with_gvisor,with_clash_api`) | legacy | lolos, tanpa peringatan |
| sing-box resmi **1.13.8** | modern | lolos, tanpa peringatan |
| sing-box resmi **1.14.4** | modern | lolos, tanpa peringatan |

Dialek dipilih otomatis dari `core version`; dipaksa dengan `--dialect legacy|modern`. Config legacy
ditolak 1.14 dan config modern ditolak 1.11 — karenanya deteksi ini penting, dan ketiga kombinasi di
atas diuji ulang lewat CI (`.github/workflows/build.yml`).

### Core sendiri (bukan binary aplikasi lain)

Core untuk pengujian dibangun dari source SagerNet dengan `scripts/build-core.sh`:

```
sing-box version v1.11.15
Environment: go1.26.0 windows/amd64
Tags: with_gvisor,with_clash_api
Revision: bc35aca01704497c179da1a03e45ad8e32f1a51b
CGO: disabled
```

`bin/PanTunnel-Core-windows-amd64.exe` — 25.552.384 byte,
SHA-256 `59d1b953f40e949137445e334dcbf5003c403aa6eb70808b2b185acd4ad2a079`.

Aplikasi menemukannya sendiri (tanpa `PANTUNNEL_CORE`) dan memilih dialek `legacy`; config hasil
`--dump-config` untuk core itu lolos `check` tanpa peringatan:

```
PanTunnel-Desktop.exe --dump-config auto-local.json --vless "vless://…#Test"
→ config ditulis ke auto-local.json (dialek: legacy)
PanTunnel-Core-windows-amd64.exe check -c auto-local.json
→ (tanpa output; exit 0)
```

> Catatan riwayat: fitur **Hotspot Share** (ICS, public = TUN) pernah ada dan **dihapus** pada
> 2026-11-24 setelah terbukti tidak bisa dipakai di Windows 11 Mobile Hotspot (tethering stack menolak
> takeover, `HRESULT 0x80040201`; driver Wi-Fi tidak mendukung SoftAP legacy). Ringkasan bukti ada di
> commit `860a4a3`.

## 3. Tunnel + DNS — terverifikasi hidup

Server uji: profil nyata `VPN-test` (`neva.jhopanstore.my.id`, VLESS WS TLS, path `/vless`), core
`bin/PanTunnel-Core-windows-amd64.exe` (sing-box 1.11.15) di laptop Windows 11 ini.

### Connect end-to-end

Jalur yang sama dengan tombol CONNECT, dijalankan lewat mode diagnosa (DNS cache di-flush dulu):

```
> PanTunnel-Desktop.exe --connect 60
profil: VPN-test (neva.jhopanstore.my.id:443/vless)
status: Connected · 3228 ms (HTTP 204)
```

`Connected` hanya dicetak setelah HTTP 204 nyata lewat TUN; exit code 0.

### Anti-DNS-leak (tunnel hidup)

| Cek | Hasil |
|---|---|
| DNS sistem (`Resolve-DnsName example.com`) | `104.20.23.154` — terjawab lewat tunnel |
| DNS adapter fisik (`-Server 192.168.10.1`) | **diblokir**, tidak ada jawaban (`strict_route` + WFP) |
| DNS di adapter TUN | `172.31.19.2` (alamat hijack sing-box) |
| `Find-NetRoute 1.1.1.1` | `pantunnel-tun` |
| HTTP 204 lewat tunnel | `204` |
| `Find-NetRoute <IP server>` | Wi-Fi, bukan TUN (anti-loop bekerja) |

Hasil sama seperti pada tunnel sing-box lain milik user di mesin ini (pembanding independen).

### Bootstrap hostname server (bug yang ketangkap saat uji ini)

Versi pertama config memakai resolver `local` untuk hostname server. Itu **deadlock**: begitu TUN naik,
resolver sistem menunjuk ke TUN sendiri, jadi resolusi hostname server berputar di dalam tunnel:

```
ERROR dns: exchange failed for neva.jhopanstore.my.id. IN A: lookup neva.jhopanstore.my.id: i/o timeout
```

Perbaikannya: `resolveServerAddress` mengunci hostname ke IP **sebelum** tunnel naik, IP itu masuk
`route_exclude_address` + aturan route `direct`, dan resolver lokal dihapus seluruhnya (kini hanya ada
resolver di dalam tunnel). Setelah perbaikan, connect berhasil walau DNS cache baru di-flush.

### Teardown

Setelah tunnel ditutup: adapter `pantunnel-tun` hilang, tidak ada proses core tertinggal, DNS adapter
fisik kembali `192.168.10.1`, dan koneksi internet normal.

## 4. Yang tidak diuji

- Skenario macOS/Linux (kode TUN-nya jalan lewat core yang sama, tapi elevasi/cap_net_admin belum
  diuji di mesin ini).
- Banyak profil VLESS server nyata (uji config memakai URI contoh dan server lokal).
- Perilaku saat `strict_route` bentrok dengan aplikasi virtualisasi (VirtualBox dsb.).
