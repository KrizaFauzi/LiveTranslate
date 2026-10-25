# LiveTranslate — kelanjutan roadmap 2026-10-23

Proyek: LiveTranslate. Vault tujuan: Obsidian Vault / Proyek/LiveTranslate/Notes/2026-10-23-roadmap-resume.md.

## Konteks yang diverifikasi dari berkas saat sesi dilanjutkan

- `docs/implementation/feature-roadmap.md` versi terbaru mencatat F3 selesai lokal; F4 tersedia dan berstatus PERLU VALIDASI untuk Chrome nyata. Ini menggantikan konteks awal percakapan yang masih menunjuk F3 sebagai SIAP DIRANCANG.
- `extension/src/audio-capture.js`, `extension/src/offscreen.js`, dan routing `audio.*` pada `extension/src/background-runtime.js` sudah tersedia. Tidak perlu membuat ulang capture.
- `docs/implementation/audio-status.md` mencatat keputusan audio tab saja, tombol mulai eksplisit, audio tidak disimpan, serta `meeting_run_id` baru setiap capture.
- Bukti live dan hasil test 2026-10-21/22 adalah catatan sesi sebelumnya dalam roadmap/status; tidak dijalankan ulang dan bukan hasil verifikasi sesi ini.
- Pengguna dalam percakapan ini kembali memilih audio tab saja serta menyetujui usulan start manual, indikator aktif/stop, capture tetap pada tab awal saat popup ditutup/fokus berpindah, dan stop saat tab ditutup/navigasi keluar meeting/logout/error. Persetujuan tersebut bukan persetujuan penyimpanan transkrip. Cakupan persetujuan ini belum diverifikasi end-to-end terhadap browser nyata.

## Fitur berikut menurut roadmap terkini

Bagian8 roadmap terkini: F4 tersedia dengan validasi native tersisa → F6 implementasi selesai lokal dengan aktivasi target/native PERLU VALIDASI → DEC-07/DEC-08 → F5 Translation. DEC-05 masih terbuka hanya untuk penyimpanan/retensi terjemahan, bukan persetujuan transkrip F6.

## Keputusan pengguna untuk F6

- Penyimpanan transkrip final bersifat opt-in per meeting/capture; default mati.
- Retensi transkrip tersimpan adalah 30 hari. Audio mentah tetap tidak disimpan.
- Timeline dan pemilihan run tersimpan memakai popup yang sudah ada, bukan halaman extension baru.
- Pemilik dapat menghapus transkrip per run dan mengekspor JSON; identitas meeting dan metadata capture tidak ikut dihapus.

Desain lengkap dan plan F6 sudah disetujui pengguna, termasuk retensi sejak awal capture, penghapusan terjadwal di Supabase, dan kegagalan penyimpanan yang membiarkan transkripsi live berjalan dengan peringatan arsip tidak lengkap. Pengguna memilih tiga slice paralel pada workspace saat ini, mempertahankan perubahan F4 yang belum di-commit. Lihat `docs/superpowers/specs/2026-10-23-transcript-storage-design.md` dan `docs/superpowers/plans/2026-10-23-transcript-storage.md`. Penyimpanan terjemahan/AI tetap di luar scope.

## Verifikasi dan sinkronisasi sesi ini

- Regresi awal `go test ./internal/audio -run '^TestSavedTranscriptRequiresOwnerAuthorization$' -count=1` gagal sebagaimana diharapkan sebelum perubahan produk: endpoint arsip mengembalikan404 untuk anonim, bukan401.
- Implementasi F6 selesai lokal: opt-in final defaultmati, retensi30hari, writer server terikat owner/run, snapshot API, timeline popup, delete terminal tanpa menghapus metadata atau mematikan capture, ekspor JSON seluruh snapshot.
- Verifikasi aktual: Go race/vet/build; extension147/147+build; auth7/7; Compose; PostgreSQL18.6 RLS/grants/reapply/concurrency; actual pg_cron1.6.8 purge; migration tanpa pg_cron gagal jelas; real Go/PostgREST16.5/PG smoke206final dalam5halaman. Smoke binary terakhir diulang setelah seluruh backend review fixes dan lulus.
- Chromium150 menjalankan React/worker aktual dengan bridge Chrome/provider/identitas terkontrol: consent/start/stop/reset, timeline50→100, JSON31.141byte/206final/through411/complete=true, delete dan opt-out berikutnya; bukan bukti tabCapture/AudioWorklet/provider nyata.
- Review data, frontend dan backend selesai; consequential findings direproduksi gagal, diperbaiki dan diverifikasi ulang. F6 tetap PERLU VALIDASI untuk migration Supabase target dan Chrome unpacked/native. Detail: `docs/implementation/transcript-storage-status.md`.
- Obsidian Local REST API tidak dapat dihubungi saat pembacaan folder proyek dan Dashboard. Konteks lokal dipakai; tidak ada klaim penulisan ke vault atau perubahan baseline sinkronisasi.
