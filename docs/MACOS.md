# macOS — status, build, dan prosedur uji

Dokumen ini untuk agent yang menjalankan uji di Mac. Windows sudah terverifikasi (lihat
`VERIFICATION.md`); macOS **belum pernah dijalankan**, jadi anggap semua langkah di bawah belum terbukti.

## Status fitur di macOS

| Bagian | Status |
|---|---|
| Build aplikasi (Gio) | Dibangun CI untuk `macos-intel` dan `macos-apple` |
| Core sing-box (utun) | `scripts/build-core.sh … darwin arm64`; butuh setuid lewat dialog admin (`runOneTimeSetup`) |
| Mode TUN + VLESS WS TLS | Jalur kode sama dengan Windows, **belum diuji di mesin Mac** |
| Anti-DNS-leak | **Belum berlaku di macOS** — lihat bagian "Celah DNS" di bawah |

## Build

```bash
brew install go        # Go ≥ 1.26; Xcode Command Line Tools wajib untuk CGO
./scripts/build-core.sh 1.11.15 darwin "$(uname -m | sed 's/x86_64/amd64/; s/arm64/arm64/')"
go build -ldflags="-s -w" -o bin/PanTunnel-Desktop-macos-apple .
```

Nama core harus persis `PanTunnel-Core-macos-apple` (arm64) atau `PanTunnel-Core-macos-intel` (x86_64) —
aplikasi mencarinya di folder yang sama dengan binary aplikasi.

Kalau binary diunduh dari GitHub (bukan build lokal), lepas quarantine dulu:

```bash
xattr -d com.apple.quarantine PanTunnel-Desktop-macos-apple PanTunnel-Core-macos-apple 2>/dev/null
chmod +x PanTunnel-Desktop-macos-apple PanTunnel-Core-macos-apple
```

## Yang aman dijalankan kapan saja (tidak menyentuh jaringan)

```bash
go vet ./... && go test ./...
bin/PanTunnel-Desktop-macos-apple --dump-config /tmp/pt.json
bin/PanTunnel-Core-macos-apple check -c /tmp/pt.json
```

`--dump-config` butuh profil: pakai profil aktif di `~/Library/Application Support/PanTunnel/settings.json`
(tidak ada di Mac baru) atau berikan URI:

```bash
bin/PanTunnel-Desktop-macos-apple --dump-config /tmp/pt.json \
  --vless "vless://UUID@host:443?type=ws&security=tls&path=/vless&sni=host&host=host#Mac"
```

## Yang butuh izin user (menaikkan utun = seluruh trafik Mac masuk tunnel)

```bash
sudo bin/PanTunnel-Desktop-macos-apple --connect 60
```

Hanya jalankan kalau user setuju. Setelah selesai, pastikan tidak ada sisa:

```bash
sudo pkill -f PanTunnel-Core || true
ifconfig | grep -A2 utun          # tidak boleh ada utun milik sesi uji
netstat -rn | grep -E '^default|0/1|128.0.0.0/1'
```

## Uji yang diminta (isi hasilnya ke `docs/VERIFICATION.md`)

1. **Config valid**: `--dump-config` → `core check` (harus tanpa `FATAL`). Dialek otomatis terdeteksi:
   core 1.11.x → legacy, ≥1.12 → modern.
2. **Tunnel hidup**: saat `--connect 60` berjalan, `ps aux | grep PanTunnel-Core` ada prosesnya dan
   `netstat -rn` menunjukkan rute default (atau `0.0.0.0/1` + `128.0.0.0/1`) lewat utun.
3. **DNS lewat tunnel**: `dig +short example.com` harus terjawab.
4. **DNS tidak bocor**: `dig @<DNS router> example.com` (mis. `dig @192.168.1.1 example.com`) harus
   **gagal/timeout**. Kalau terjawab → dicatat sebagai **temuan leak** (lihat bagian berikut).
5. **HTTP lewat tunnel**: `curl -s -o /dev/null -w '%{http_code}\n' http://connectivitycheck.gstatic.com/generate_204`
   → `204`.
6. **Teardown**: setelah proses keluar, utun hilang, `dig` kembali memakai router, internet normal.

## Celah DNS di macOS (kemungkinan besar bocor)

Di Windows, sing-box menyetel DNS di adapter TUN (`172.31.19.2`), jadi query host masuk tunnel dan
di-hijack. Di macOS sing-box **tidak** menyetel resolver sistem, sementara `config` kita mengecualikan
`192.168.0.0/16` dari rute TUN. Artinya query ke DNS router bisa keluar langsung = **DNS leak**.

Dua cara memperbaiki (pilih satu, lalu uji ulang poin 4):

- Arahkan resolver sistem ke alamat TUN saat connect dan kembalikan saat disconnect:
  `sudo networksetup -setdnsservers "Wi-Fi" 172.31.19.2` … `sudo networksetup -setdnsservers "Wi-Fi" Empty`.
- Atau masukkan IP resolver router ke `route_address` (lebih spesifik daripada `route_exclude_address`)
  supaya query-nya masuk TUN dan dijawab `hijack-dns`.

Perbaikan ini belum diimplementasi. Jangan klaim "tanpa DNS leak" di macOS sebelum poin 4 lulus.

## Aturan untuk agent

- Jangan menaikkan utun (`--connect`, tombol CONNECT, `sudo core run`) tanpa izin eksplisit user.
- Jangan mengubah Internet Sharing/NAT (`com.apple.nat.plist`, `networksharingd`) tanpa izin user.
- Setelah uji, pastikan core mati, utun hilang, DNS sistem kembali seperti semula.
- Laporkan hasil apa adanya ke `docs/VERIFICATION.md` beserta perintah dan output aslinya.