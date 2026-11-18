# Hotspot Share — klien hotspot langsung pakai VPN

Fitur ini membuat perangkat yang tersambung ke **Mobile Hotspot** laptop ikut keluar lewat tunnel VPN,
tanpa aplikasi apa pun di sisi klien.

## Kenapa perlu trik

Windows memberi DHCP + NAT ke klien hotspot lewat **Internet Connection Sharing (ICS)**. ICS selalu
membagikan **satu** koneksi yang ditandai sebagai *public*. Kalau yang ditandai public adalah adapter
fisik (Wi-Fi/USB tethering), klien hotspot keluar lewat jalur itu — VPN-nya dilewati, termasuk DNS-nya.
Itu bocor yang paling sering terjadi: "VPN hidup di laptop, tapi HP yang nebeng hotspot lihat IP asli".

PanTunnel Desktop menandai **TUN** sebagai koneksi public:

```
klien hotspot (192.168.137.x)
      │  DHCP + NAT dari ICS
      ▼
pantunnel-tun  ── VLESS WS TLS ──▶ server
      ▲
      └── ICS public = pantunnel-tun
```

Karena ICS memakai NAT penuh, paket klien masuk ke TUN dengan alamat TUN sebagai sumber. Bagi sing-box
itu trafik biasa: destinasi publik → lewat VLESS. DNS klien (ke gateway 192.168.137.1) diteruskan ICS
ke resolver sistem, yang sudah dipaksa lewat TUN dan di-hijack — jadi tidak ada DNS leak di sisi klien.

## Cara pakai

1. Nyalakan **Mobile Hotspot** di Windows (Pengaturan → Jaringan & internet → Hotspot seluler).
2. Nyalakan tunnel: tekan **CONNECT**.
3. Tekan **AKTIFKAN** di kartu **HOTSPOT SHARE**.
4. Kartu akan berubah menjadi `Hotspot share: aktif — N klien lewat VPN`.

Saat **DISCONNECT**, koneksi yang tadinya berbagi internet dikembalikan seperti semula
(dinamai `RESTORE=` di CLI, disimpan sebagai `hotspot_restore` di `settings.json`).

## Yang terjadi di belakang

`hotspot_windows.go` menjalankan skrip PowerShell kecil (`%AppData%\PanTunnel\ics.ps1`) yang memakai
objek COM `HNetCfg.HNetShare`:

| Aksi | Isi |
|---|---|
| `query` | daftar koneksi ICS + siapa public/private + adapter Wi-Fi Direct + jumlah klien di `192.168.137.0/24` |
| `enable` | matikan sharing public pada koneksi lain → `EnableSharing(0)` pada `pantunnel-tun` → `EnableSharing(1)` pada adapter hotspot kalau ada |
| `disable` | `DisableSharing()` pada TUN → kembalikan sharing public ke koneksi sebelumnya |

Watchdog 15 detik (`hotspotWatch`) memeriksa ulang dan mengambil alih lagi kalau Windows mengembalikan
sharing ke adapter fisik, misalnya setelah hotspot dimatikan-nyalakan atau jaringan berganti.

Tidak ada perubahan registry dan tidak ada driver tambahan: hanya state ICS yang memang dikelola
Windows, dan semuanya bisa dikembalikan.

## Prasyarat

- Windows (ICS tidak ada di Linux/macOS; di platform itu fitur ditandai tidak didukung, aplikasi tetap
  jalan untuk TUN biasa).
- **Administrator** — aplikasi melakukan UAC sendiri saat start.
- Layanan `Internet Connection Sharing (ICS)` aktif (default di Windows).

## Batas yang diketahui

- Kalau tunnel belum tersambung, tombol AKTIFKAN tetap bisa ditekan tetapi sharing ke TUN tidak
  berguna; tombol paling aman ditekan setelah status `Connected`.
- Saat tunnel gagal, PanTunnel otomatis mengembalikan sharing ke koneksi sebelumnya — kalau prosesnya
  dimatikan paksa (`kill -9`), sharing bisa tertinggal di TUN; kembalikan manual lewat
  Pengaturan Windows → Jaringan → adapter → Properties → Sharing, atau `--ics disable`.
- Adapter hotspot yang dipakai adalah **Microsoft Wi-Fi Direct Virtual Adapter** ("Local Area
  Connection* N"). USB tethering / Ethernet tetap memakai ICS biasa dan tidak termasuk di fitur ini.
- ICS hanya untuk IPv4; IPv6 klien tidak dibagikan Windows dan tidak dijanjikan di sini.

## Uji manual (butuh izin user — mengubah state jaringan)

```bash
PanTunnel-Desktop.exe --ics query     # baca status, tidak mengubah apa pun
PanTunnel-Desktop.exe --ics enable    # cetak RESTORE=<nama koneksi sebelumnya>
PanTunnel-Desktop.exe --ics disable
```

Uji otomatis yang tidak menyentuh jaringan: `go test ./...` memeriksa sintaks skrip ICS memakai parser
PowerShell asli, dan `parseHotspotQuery` diuji terhadap laporan nyata (`PUBLIC=`, `CLIENTS=`, `ERROR=`).
