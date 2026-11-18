<div align="center">

# PanTunnel Desktop

**VPN ringan desktop berbasis sing-box — VLESS WebSocket TLS**

Turunan desktop dari [JPS Tunnel](https://github.com/jhopan/JPS-Tunnel) · fokus Windows, TUN, tanpa DNS leak, plus Hotspot Share

</div>

---

## Tentang

PanTunnel Desktop adalah klien VPN untuk laptop/PC yang menjalankan [sing-box](https://github.com/SagerNet/sing-box)
sebagai proses core dan menyalurkan **seluruh trafik sistem** lewat TUN. Hanya satu protokol yang
didukung — **VLESS + WebSocket + TLS** — supaya tidak ada jalur yang setengah jalan.

Perbedaan utama dari JPS Tunnel Desktop:

| | JPS Tunnel Desktop | PanTunnel Desktop |
|---|---|---|
| Mode jaringan | Tun + mixed proxy 127.0.0.1:10808 | **TUN saja** — tidak ada proxy lokal |
| DNS | Server DNS lewat tunnel | DNS lewat tunnel **+ hijack-dns** + `strict_route` |
| Kebocoran IPv6 | Route IPv6 tidak dikelola | Alamat IPv6 di TUN, trafik gagal = ditolak (fail-closed) |
| Subnet TUN | `172.19.0.1/30` | `172.31.19.1/30` (hindari bentrok dengan klien sing-box lain) |
| Hotspot laptop | — | **Hotspot Share**: klien hotspot ikut lewat VPN |
| Status | "Connected" saat proses core hidup | "Connected" hanya setelah HTTP 204 nyata lewat TUN |
| Dialek config core | 1.11.x saja | otomatis: 1.11 (legacy) atau ≥1.12 (modern) |
| Elevasi | jalankan manual sebagai Administrator | UAC otomatis saat start |

---

## Fitur

### Koneksi
- VLESS + WebSocket + TLS · SNI, Host, Path dihormati persis dari URI
- TUN gVisor (`pantunnel-tun`) — semua trafik sistem, tidak ada aplikasi yang perlu diarahkan manual
- Status jujur: **Connecting → Connected**, dan `Connected` hanya muncul setelah HTTP 204 benar-benar
  lewat tunnel (sekaligus membuktikan jalur DNS di dalam tunnel hidup)
- Kalau tunnel tidak menjawab setelah 3 percobaan, core dimatikan dan status menjadi `GAGAL:` dengan
  pesan error aslinya — bukan "Connected" palsu

### Tanpa DNS Leak
- `strict_route: true` — filter WFP Windows menghentikan kebiasaan *multihomed DNS* bawaan Windows
- `hijack-dns` — semua paket DNS yang masuk TUN dijawab resolver di dalam tunnel
- Tidak ada resolver cadangan di luar tunnel; resolver `local` hanya untuk bootstrap hostname server VPN
- IPv6 di-fail-closed, bukan dibocorkan ke adapter fisik

Detail dan cara mengujinya: [`docs/DNS-LEAK.md`](docs/DNS-LEAK.md).

### Hotspot Share (baru)
- Laptop jadi hotspot (Mobile Hotspot Windows) → klien hotspot **langsung pakai VPN**
- Tunnel dijadikan koneksi yang dibagikan Windows (ICS public = `pantunnel-tun`), bukan adapter fisik
- DHCP + NAT tetap dari Windows; klien dapat IP `192.168.137.x`, trafiknya keluar lewat VPN
- Jumlah klien yang lewat VPN ditampilkan di kartu Hotspot Share
- Saat disconnect, koneksi yang tadinya berbagi internet dikembalikan seperti semula
- Watchdog 15 detik: kalau Windows mengembalikan sharing ke adapter fisik, tunnel diambil alih lagi

Detail: [`docs/HOTSPOT.md`](docs/HOTSPOT.md).

### Profil & Core
- Multi-profil — import dari clipboard (`vless://`) atau file `.txt`/`.jps`
- Kartu profil: Pilih / Edit / Hapus, profil aktif ditandai border hijau
- Dialek config dideteksi otomatis dari `core version` (legacy untuk sing-box 1.11.x, modern untuk ≥1.12)
- Core dicari di folder aplikasi: `PanTunnel-Core-windows-amd64.exe`, `JPS-Tunnel-Core-windows-amd64.exe`,
  atau `sing-box.exe`. Bisa juga dipaksa lewat env `PANTUNNEL_CORE`.

---

## Core: dibangun sendiri dari source

PanTunnel Desktop tidak menanam core di dalam binary, dan **tidak memakai binary sing-box dari aplikasi lain**.
Core dibangun dari source resmi SagerNet (MIT) dengan tag yang dibutuhkan:

```bash
./scripts/build-core.sh                 # host ini → bin/PanTunnel-Core-windows-amd64.exe
./scripts/build-core.sh 1.11.15 windows amd64
./scripts/build-core.sh 1.11.15 linux amd64
```

Script itu menjalankan, persis:

```bash
git clone --depth 1 --branch v1.11.15 https://github.com/SagerNet/sing-box.git
cd sing-box
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
  -tags "with_gvisor,with_clash_api" \
  -ldflags "-s -w -X github.com/sagernet/sing-box/constant.Version=v1.11.15" \
  -o bin/PanTunnel-Core-windows-amd64.exe ./cmd/sing-box
```

Tag wajib: `with_gvisor` (stack TUN yang dipakai config) dan `with_clash_api`.

Hasil build di mesin ini:

| | |
|---|---|
| File | `bin/PanTunnel-Core-windows-amd64.exe` |
| Versi | sing-box **v1.11.15** (`Tags: with_gvisor,with_clash_api`, revision `bc35aca`) |
| Ukuran | 25.552.384 byte |
| SHA-256 | `59d1b953f40e949137445e334dcbf5003c403aa6eb70808b2b185acd4ad2a079` |

`bin/` ada di `.gitignore`: binary tidak masuk git, tapi bisa dibangun ulang kapan saja dengan script di atas.
CI juga membangun core untuk windows/linux/macos dan menyertakannya di setiap release.

Aplikasi mencari core di folder yang sama dengan exe, berurutan:
`PanTunnel-Core-windows-amd64.exe` → `JPS-Tunnel-Core-windows-amd64.exe` → `sing-box.exe`,
dan bisa dipaksa lewat env `PANTUNNEL_CORE`. Dialek config dipilih otomatis dari `core version`, jadi core
1.13/1.14 resmi juga jalan (dialek modern).

---

## Build

```bash
go vet ./...
go test ./...
go build -ldflags="-s -w -H windowsgui" -o bin/PanTunnel-Desktop.exe .
```

Installer Windows (butuh Inno Setup 6):

```bash
"C:/Program Files (x86)/Inno Setup 6/ISCC.exe" setup.iss
```

## CLI (untuk diagnosa & CI)

```bash
# tulis config yang persis dipakai GUI, tanpa menjalankan aplikasi
PanTunnel-Desktop.exe --dump-config out.json --vless "vless://uuid@host:443?type=ws&security=tls&path=/vless&sni=host&host=host#Nama"
PanTunnel-Desktop.exe --dump-config out.json --dialect legacy   # paksa dialek 1.11
PanTunnel-Desktop.exe --ics query                               # status ICS / hotspot (Windows)
```

Perintah `--dump-config` + `sing-box check` adalah gerbang verifikasi config: tidak menyentuh jaringan,
tidak butuh admin.

## Data aplikasi

`%AppData%\PanTunnel\` — `settings.json`, `config.json` (generated), `cache.db`, `ics.ps1`.

## Test

```bash
go test ./...
```

Mencakup: parser URI VLESS, bentuk config anti-leak (TUN saja, `strict_route`, `hijack-dns`, DNS lewat
tunnel, LAN di-exclude), perbedaan dialek legacy/modern, parsing versi core, parser laporan ICS, dan
pemeriksaan sintaks skrip PowerShell ICS memakai parser PowerShell asli.

Hasil verifikasi config terhadap core asli: [`docs/VERIFICATION.md`](docs/VERIFICATION.md).

## Lisensi

MIT License · © 2026 JhopanStore
