# Tanpa DNS Leak — bagaimana dan kenapa

Klien VPN desktop biasanya bocor di tiga tempat. PanTunnel Desktop menutup ketiganya; dokumen ini
menjelaskan mekanismenya dan cara membuktikannya **tanpa** mengganggu jaringan yang sedang dipakai.

## 1. DNS lewat adapter fisik (multihomed DNS Windows)

Windows mengirim query DNS ke **semua** adapter yang punya resolver, lalu memakai jawaban tercepat.
Akibatnya query bisa dijawab router/ISP walau VPN sudah tersambung.

Yang dipakai PanTunnel:

```json
"inbounds": [{ "type": "tun", "strict_route": true, "auto_route": true, ... }]
```

`strict_route: true` di Windows memasang filter WFP yang mematikan perilaku tersebut: paket DNS yang
tidak lewat TUN tidak dijawab. Dokumentasi sing-box menyebutnya eksplisit — *"In Windows: … prevent DNS
leak caused by Windows' ordinary multihomed DNS resolution behavior"*.

Konsekuensinya: aplikasi seperti VirtualBox bisa bermasalah pada sebagian konfigurasi. Itu harga yang
dibayar untuk fail-closed, dan dicatat di sini supaya tidak jadi kejutan.

## 2. Resolver cadangan di luar tunnel

Config yang dihasilkan hanya punya resolver di dalam tunnel:

```json
"servers": [
  { "tag": "dns-tunnel",  "detour": "proxy", ... "server": "1.1.1.1" },
  { "tag": "dns-backup",  "detour": "proxy", ... "server": "8.8.8.8" },
  { "tag": "dns-local",   "type": "local" }          // bootstrap saja
],
"final": "dns-tunnel",
"rules": [{ "domain": ["<hostname server VPN>"], "server": "dns-local" }]
```

`dns-local` hanya menjawab satu nama: hostname server VPN itu sendiri, yang memang harus diresolusi
sebelum tunnel ada. Semua nama lain (`final`) lewat `proxy`.

## 3. Query DNS dari aplikasi yang memilih server sendiri

Ada aplikasi yang mengirim query ke `8.8.8.8`, `1.1.1.1`, atau DNS router secara eksplisit. Untuk itu
TUN mem-hijack seluruh paket DNS:

```json
"route": { "rules": [
  { "action": "sniff" },
  { "action": "hijack-dns", "protocol": "dns" },
  ...
] }
```

Paket DNS apa pun yang masuk TUN dijawab oleh resolver di dalam tunnel, tanpa peduli alamat tujuannya.

## Kebocoran IPv6

TUN diberi alamat IPv6 (`fdfe:dcba:9876::1/126`). Efeknya sing-box juga memasang route IPv6 ke TUN,
sehingga trafik IPv6 yang tidak bisa lewat tunnel **gagal di dalam tunnel**, bukan menyelinap lewat
adapter fisik. Ini pilihan fail-closed: lebih baik IPv6 mati daripada bocor.

## LAN tetap lokal

```json
"route_exclude_address": ["127.0.0.0/8","10.0.0.0/8","172.16.0.0/12","192.168.0.0/16","169.254.0.0/16"]
```

Trafik ke perangkat lokal, printer, router, dan **klien hotspot** tidak ditarik ke tunnel. Ini wajib:
subnet hotspot Windows (192.168.137.0/24) harus tetap dijangkau langsung supaya DHCP dan gateway
hotspot bekerja (lihat `HOTSPOT.md`).

## Cara membuktikan (jangan dijalankan tanpa izin user)

Uji ini menarik seluruh trafik laptop ke tunnel — **jangan** dijalankan saat user sedang memakai
jaringan. Jalankan hanya dengan izin eksplisit.

```bash
# 1. config untuk core modern, tanpa menjalankan apa pun
PanTunnel-Desktop.exe --dump-config /tmp/pt.json \
  --vless "vless://uuid@host:443?type=ws&security=tls&path=/vless&sni=host&host=host#Nama"
sing-box check -c /tmp/pt.json          # harus lolos tanpa FATAL

# 2. jalankan tunnel (butuh admin), lalu periksa:
Get-NetAdapter pantunnel-tun                                  # harus Up
Get-DnsClientServerAddress -InterfaceAlias pantunnel-tun       # DNS = alamat TUN
Resolve-DnsName example.com                                    # harus terjawab (lewat tunnel)
Resolve-DnsName example.com -Server <DNS adapter fisik>        # harus timeout / gagal
Find-NetRoute -RemoteIPAddress 1.1.1.1                         # harus lewat pantunnel-tun
```

Log core pada `log.level=debug` menunjukkan buktinya baris per baris:

```
INFO inbound/tun[tun-in]: inbound DNS packet from 172.31.19.1:57688
DEBUG dns: exchange example.com. IN A
INFO outbound/vless[proxy]: outbound packet connection to 1.1.1.1:53
```

Artinya query DNS masuk ke TUN, bukan keluar lewat adapter fisik.

## Batas yang diketahui

- `strict_route` bisa mengganggu aplikasi yang butuh akses adapter mentah (VirtualBox, sebagian alat
  virtualisasi/emulator).
- Resolver `local` untuk hostname server VPN sengaja dipertahankan; kalau hostname itu diracuni DNS
  lokal, koneksi bisa diarahkan salah. Pakai IP server kalau ini mengganggu.
- DNS-over-HTTPS bawaan browser (Chrome/Firefox "secure DNS") mengirim DoH ke port 443 dan ikut masuk
  tunnel, tapi resolvernya ditentukan browser, bukan PanTunnel. Untuk pemeriksaan paling ketat,
  matikan DoH di browser.
