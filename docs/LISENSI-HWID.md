# Lisensi HWID (.pantunnel)

Profil bisa dikirim sebagai **file lisensi** yang hanya bisa dibuka perangkat dengan HWID tertentu,
opsional dengan tanggal kedaluwarsa. Ini jalan supaya HWID di layar Pengaturan benar-benar berguna.

## Alur

```
pelanggan                          admin
─────────                          ─────
buka Pengaturan → SALIN HWID  ──▶  PanTunnel-Desktop.exe --license-create \
                                     uji.pantunnel --hwid <HWID> \
                                     --vless "vless://…" --name "Pelanggan" --expire 2027-12-31
terima file .pantunnel  ◀────────  kirim file (WhatsApp/Telegram/email)
tombol File → pilih .pantunnel  →  profil masuk daftar, lisensi tercatat
```

Di GUI **tidak ada tombol export** — file lisensi hanya dibuat oleh tooling admin di terminal
(`--license-create`). Di sisi pelanggan hanya ada import (tombol **File**, atau **Clipboard** kalau isi
filenya ditempel).

## Perintah admin

```bash
# buat lisensi untuk satu pelanggan (HWID dari layar Pengaturan mereka)
PanTunnel-Desktop.exe --license-create pelanggan.pantunnel \
  --hwid 453A6791CD2ED2731FF6E5E5 \
  --vless "vless://uuid@host:443?type=ws&security=tls&path=/vless&sni=host&host=host#Pelanggan" \
  --name "Pelanggan A" --expire 2027-12-31

# periksa sebelum dikirim (read-only)
PanTunnel-Desktop.exe --license-info pelanggan.pantunnel
PanTunnel-Desktop.exe --license-info pelanggan.pantunnel --hwid 453A6791CD2ED2731FF6E5E5

# HWID juga bisa dari env
PANTUNNEL_HWID=453A… PanTunnel-Desktop.exe --license-create pelanggan.pantunnel --vless "vless://…"
```

`--expire` kosong = tanpa batas waktu. Format tanggal `YYYY-MM-DD`; lisensi berlaku **sampai akhir hari
itu**.

## Format file

```json
{
  "v": 1,
  "hwid": "453A6791CD2ED2731FF6E5E5",
  "expire": "2027-12-31",
  "nonce": "base64…",
  "data": "base64…"
}
```

- `data` = AES-256-GCM dari payload `{name, uri, expire, issued}`.
- Kunci = `SHA-256("PanTunnel-license-v1|" + HWID ternormalisasi)`; HWID dinormalisasi (huruf besar,
  tanpa spasi/tanda hubung) sehingga HWID yang disalin dari layar selalu cocok.
- Header (`v`, `hwid`, `expire`) ikut ditandatangani sebagai AAD, jadi **mengubah tanggal
  kedaluwarsa membuat file tidak sah** — tag GCM gagal.
- Profil yang masuk dari lisensi membawa `license_expire` di `settings.json`; tombol CONNECT menolak
  profil yang lisensinya sudah lewat.

## Batas keamanan (baca ini sebelum menjual)

Ini **bukan DRM kriptografis**. Kunci diturunkan dari HWID, dan HWID ditampilkan di aplikasi, jadi
siapa pun yang tahu HWID seseorang bisa membuat file untuk HWID itu. Yang benar-benar didapat:

| Terlindungi | Tidak terlindungi |
|---|---|
| File tidak jalan di perangkat lain (tag GCM gagal) | Siapa pun yang tahu HWID + algoritma bisa membuat file sendiri |
| Tanggal kedaluwarsa tidak bisa diperpanjang tanpa kunci | Fitur ini tidak menghubungi server, jadi tidak ada pencabutan jarak jauh |
| Pelanggan tidak bisa "export" dari GUI | Menyalin `settings.json` ke mesin lain ikut menyalin `installation_id` → HWID-nya sama |

Karena itu HWID di sini adalah **ID instalasi**, bukan ID perangkat keras: `makeHWID` menurunkan dari
`installation_id` acak yang disimpan di `settings.json`. Itu konsisten dengan desain Android (lisensi
offline, tanpa server), tapi jangan dianggap sebagai penjaga anti-berbagi yang kuat.

Kalau nanti butuh yang lebih kuat: minting harus memakai kunci privat (mis. Ed25519) yang tidak ada di
binary pelanggan, dan verifikasi memakai kunci publik. Itu perubahan format + tooling terpisah — belum
dikerjakan.

## Uji

```bash
go test -run 'License' -v ./...
```

Mencakup: round-trip, normalisasi HWID, penolakan HWID lain, penolakan kedaluwarsa (termasuk batas
akhir hari), penolakan header yang diubah, validasi input minting, deteksi file lisensi vs URI biasa,
dan jalur import lengkap (file → profil → `settings.json`) memakai `PANTUNNEL_DATA_DIR` ke folder
sementara.
