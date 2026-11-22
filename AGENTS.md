# AGENTS.md — PanTunnel Desktop

Aturan kerja khusus repo ini.

## Jangan sentuh jaringan user tanpa izin

Repo ini berisi klien TUN: menjalankannya berarti memindahkan **seluruh** trafik laptop ke tunnel, dan
`strict_route` memasang filter WFP. Itu bisa memutus koneksi user yang sedang kerja (termasuk sesi
remote ke agen ini).

- **MUST NOT** menjalankan core dengan config TUN (`sing-box run -c ...`) tanpa izin eksplisit user
  pada sesi itu.
- **MUST NOT** mengubah state ICS (`--ics enable`, `--ics disable`) tanpa izin eksplisit: itu
  mengubah sharing internet laptop.
- **MUST NOT** menjalankan `--connect`: mode itu menaikkan TUN, sama seperti menekan CONNECT.
- Aman dijalankan kapan saja: `go test ./...`, `go vet ./...`, `--dump-config` + `sing-box check`,
  `--ics query` (read-only).
- Kalau tunnel pernah dijalankan saat menguji: pastikan core mati, adapter `pantunnel-tun` hilang, dan
  koneksi user pulih sebelum melapor selesai.

## Git: push & rilis hanya saat diminta

| Aksi | Kapan |
|---|---|
| `git commit` lokal | setelah satu fitur/perbaikan selesai dan `go vet`/`go test` hijau (aturan workspace) |
| `git push` / `git tag` / `gh release create` | **hanya** setelah user minta — satu push ke `main` langsung memicu CI |

Setelah selesai kerja: laporkan perubahan (file apa, apa yang berubah, hasil `go test`/`go vet`),
lalu berhenti dan tunggu perintah. Working tree boleh kotor — itu normal.

## Build & test

```bash
go vet ./...
go test ./...
go build -ldflags="-s -w -H windowsgui" -o bin/PanTunnel-Desktop.exe .
```

Gio butuh CGO (gcc sudah ada di mesin ini: `C:\TDM-GCC-64\bin\gcc.exe`).

## Gerbang verifikasi sebelum lapor "selesai"

1. `go vet ./...` bersih, `go test ./...` hijau.
2. `--dump-config` untuk kedua dialek, lalu `sing-box check` dengan core sungguhan:
   `bin/PanTunnel-Core-windows-amd64.exe` (bangun dengan `./scripts/build-core.sh`) untuk dialek legacy,
   sing-box ≥1.12 untuk dialek modern.
3. Perubahan UI/ICS: uji hidup hanya dengan izin user (lihat aturan di atas), dan catat apa yang
   belum diuji di `docs/VERIFICATION.md`.

## Aturan kode

- TUN saja. Jangan hidupkan lagi inbound `mixed`/proxy lokal: itu jalur kedua yang setengah jalan dan
  sumber kebocoran.
- Setiap penambahan konfigurasi anti-leak harus punya test di `singbox_test.go` yang gagal kalau field
  itu hilang.
- Skrip PowerShell ICS diedit sebagai string Go: jalankan `go test -run TestICSScript` setelah mengubah.
- Bahasa UI: Indonesia, istilah teknis tidak diterjemahkan.
