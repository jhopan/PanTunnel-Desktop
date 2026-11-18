# Verifikasi

Bukti yang sudah ada di repo ini, dan yang **belum** dikerjakan. Ditulis apa adanya: yang belum diuji
disebut belum diuji.

## 1. Test unit — `go test ./...`

```
--- PASS: TestICSScriptParses            (parser PowerShell asli memeriksa skrip ICS)
--- PASS: TestICSScriptSharesTunAndRestores
--- PASS: TestParseVLESSWebSocketTLS
--- PASS: TestRejectNonWebSocketTLS
--- PASS: TestParsePort
--- PASS: TestConfigLeakProtection
--- PASS: TestConfigDialects
--- PASS: TestParseCoreVersion
--- PASS: TestParseHotspotQuery
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

## 3. Tunnel + DNS — sebagian, tanpa uji hidup penuh

Diuji pada mesin pengembang dengan core sing-box 1.14.4 sebagai klien dan sing-box 1.14.4 sebagai
server VLESS WS TLS lokal, memakai config hasil `--dump-config`. Yang terbukti dari log core
(`log.level=debug`):

```
INFO inbound/tun[tun-in]: started at pantunnel-tun
INFO inbound/tun[tun-in]: inbound connection to 142.251.157.119:443
INFO outbound/vless[proxy]: outbound connection to 142.251.157.119:443
INFO inbound/tun[tun-in]: inbound DNS packet from 172.31.19.1:57688
DEBUG dns: exchange example.com. IN A
INFO outbound/vless[proxy]: outbound packet connection to 1.1.1.1:53
```

- TUN `pantunnel-tun` benar-benar dibuat dan menerima trafik sistem.
- Trafik TCP masuk TUN → sniff → keluar lewat outbound VLESS (bukan adapter fisik).
- Query DNS masuk TUN dan dijawab resolver di dalam tunnel (`hijack-dns` bekerja).

Dua hal yang **belum** diverifikasi hidup, karena uji ini memindahkan seluruh trafik laptop dan user
meminta tidak dijalankan dulu ("bakal ganggu koneksi ku"):

1. Query DNS langsung ke resolver adapter fisik harus gagal (`strict_route`). Sebelumnya mekanisme ini
   terbukti bekerja di mesin yang sama pada tunnel sing-box lain yang memakai `strict_route`.
2. **Hotspot Share** end-to-end: ICS public = `pantunnel-tun` dan klien hotspot keluar lewat VPN.
   Yang sudah ada: skrip ICS lolos parser PowerShell, unit test parsing laporan ICS, dan pembacaan
   status ICS hidup (`--ics query`) pada mesin pengembang — tanpa mengubah state.

Langkah uji hidup ada di `docs/DNS-LEAK.md` dan `docs/HOTSPOT.md`. Jalankan hanya dengan izin user dan
saat laptop tidak sedang dipakai kerja.

## 4. Yang tidak diuji

- Skenario macOS/Linux (kode TUN-nya jalan lewat core yang sama, tapi elevasi/cap_net_admin belum
  diuji di mesin ini).
- Banyak profil VLESS server nyata (uji config memakai URI contoh dan server lokal).
- Perilaku saat `strict_route` bentrok dengan aplikasi virtualisasi (VirtualBox dsb.).
