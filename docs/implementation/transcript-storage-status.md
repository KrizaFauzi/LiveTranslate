# F6 — Transkrip tersimpan dan timeline

Tanggal verifikasi: 2026-10-24 waktu lokal (2026-10-23 UTC).
Status roadmap: **PERLU VALIDASI** — kode, suite, build, database dan smoke UI/API lokal tersedia; migration project Supabase target dan Chrome unpacked/native belum diterapkan/divalidasi pada sesi ini. Bukan klaim siap produksi.

Desain dan plan disetujui pengguna: [spec](../superpowers/specs/2026-10-23-transcript-storage-design.md), [plan](../superpowers/plans/2026-10-23-transcript-storage.md).

## Perilaku yang diimplementasikan

- Audio tab saja; tidak mengakses mikrofon dan tidak menyimpan audio mentah atau partial.
- Checkbox simpan final default mati, berlaku satu capture dan kembali mati untuk capture berikutnya. Backend menerima boolean optional `save_transcript`; worker tetap memilih meeting/tab sendiri.
- Final disimpan langsung dari canonical publication backend sebelum SSE fan-out, bukan dari backlog100 atau cache live200. Sequence final mempertahankan gap partial.
- `transcript_segments` memiliki owner, meeting dan run yang konsisten, UUID/timestamp, final immutable dan unique `(run_id,seq)`. Duplikat identik idempoten; isi berbeda pada seq sama ditolak.
- Writer terurut dibatasi128 final atau2MiB teks queued, request10detik dan finalisasi/drain30detik. Penyimpanan gagal tidak sendiri menghentikan live capture; arsip failed/incomplete tidak dilaporkan saved. Tidak ada retry/outbox otomatis atau jaminan crash-proof sebelum acknowledgment.
- Kredensial writer tetap JWT pengguna di memori, diperbarui hanya setelah verifikasi token/ownership dengan ticket monoton. Tidak ada refresh token backend atau service-role key.
- Status capture terpisah dari storage. Stop/provider-end menunggu final yang sudah diterima dan drain; status saved memerlukan acknowledgment database, bukan sekadar popup menerima teks atau stop HTTP sukses.
- Read/list per run memakai ownership, no-store, keyset pagination dan snapshot `through_seq`, termasuk0. Snapshot lama/aktif tidak dipromosikan menjadi lengkap hanya karena writer selesai saat request berjalan.
- Timeline tersimpan di popup yang ada, terpisah dari Live; polling status tidak membuang halaman yang sudah dimuat. Ekspor JSON mengambil seluruh snapshot, bukan hanya halaman terlihat/cache200.
- Delete transkrip terminal dan idempoten, termasuk saat capture aktif. Parent-row lock, database guard/RLS dan state transition mencegah append queued/in-flight menghidupkan kembali isi. Run, meeting dan counter tetap ada; live boleh terus berjalan.
- Pending delete bertahan lintas Live/reselection run yang sama. Pembacaan/ekspor yang mengetahui deleted/expired membatalkan operasi lama; logout/account/context change membersihkan/fence teks dan unduhan lama. Credential error tidak mencegah local offscreen/media cleanup.

## Retensi dan batas trust

Expiry dihitung database dari `started_at + interval '30 days'`, immutable. RLS/read/write menggunakan waktu database dan write memeriksa `clock_timestamp()` sesudah memperoleh lock. Setelah expiry, isi tidak dapat dibaca/ditulis bahkan sebelum purge fisik.

Migration0005 memasang job nyata `livetranslate-transcript-retention`, jadwal `* * * * *`, command `SELECT public.purge_expired_transcripts();`. Satu job aktif setelah reapply; extension/scheduler tidak tersedia menghasilkan error, bukan no-op. Scheduler yang sehat melakukan purge pada putaran menit berikutnya; outage dapat menunda penghapusan fisik. Run/meeting tidak ikut dihapus.

RPC aplikasi SECURITY INVOKER; grants Data API langsung tetap dibatasi guard/RLS. Pemilik juga dapat memanggil Data API memakai JWT-nya sendiri: ownership/consent/retensi dijamin database, tetapi provenance provider/keaslian percakapan terhadap pemilik sendiri bukan jaminan database. Tidak memberi akses admin pada aplikasi.

Salinan JSON yang sudah diunduh, backup/PITR Supabase dan log provider mengikuti kepemilikan/kebijakan layanan masing-masing, bukan janji penghapusan F6.

## Berkas utama

- `migration/0004_transcript_segments.sql`: metadata consent, schema, RLS, guard, append/delete/state RPC dan admin purge.
- `migration/0005_transcript_retention_cron.sql`: aktivasi scheduler terpisah dan job idempoten.
- `migration/tests/transcript_segments.sql`, `transcript_concurrency.sh`: real PostgreSQL ownership, deletion/append/expiry races dan purge.
- `backend/internal/transcribe/{session,broker}.go`: early final sink, seq dan shutdown.
- `backend/internal/audio/{transcript_writer,archive,archive_repository}.go`: persistence, lifecycle dan API.
- `extension/src/saved-transcripts.js`, `App.jsx`, `background-runtime.js`: authenticated archive client, snapshot/export/timeline dan context isolation.
- `extension/src/audio-capture.js`, `offscreen.js`, `transcript-state.js`: per-run consent dan owner/run-bound live lifecycle.

## Verifikasi aktual sesi ini

| Pemeriksaan | Hasil | Bukti dan batas |
| --- | --- | --- |
| `go test -race -count=1 -timeout=120s ./...` | LULUS | Seluruh paket Go, termasuk regresi finalisasi/stop/snapshot dan account/DB failure |
| `go vet ./...` dan build server | LULUS | Binary backend dan binary smoke terkontrol berhasil dibangun |
| `npm test` | LULUS |147/147 test; mencakup revoked-refresh cleanup, delete/reselection, reciprocal terminal export/read, polling extent dan list-loading race |
| `npm run build` | LULUS | Vite7.3.7 menghasilkan popup, background dan offscreen |
| Auth contract | LULUS |7/7 melalui `node --test --test-isolation=none http/auth.test.mjs` |
| `docker compose config --quiet` | LULUS | Compose tidak diubah untuk menambah service |
| `sh migration/tests/check-meetings.sh` | LULUS | PostgreSQL18.6 throwaway: reapply, RLS/grants, consent, immutable finals, direct mutation, purge dan FK cascade |
| Controlled SQL concurrency | LULUS | Harness mengamati actual parent-lock wait sebelum release; delete/append dua urutan dan expiry lewat RPC/direct insert |
| Migration0005 + scheduler lokal | LULUS | PostgreSQL18.6 + pg_cron1.6.8; reapply menghasilkan satu job aktif; actual scheduled purge menghapus teks expired, mempertahankan metadata run dan final belum expired |
| Scheduler tidak tersedia | LULUS | Migration0005 benar-benar dijalankan pada PostgreSQL18.6 tanpa pg_cron: gagal jelas dengan `transcript retention requires pg_cron`, bukan sukses tanpa purge |
| HTTP/API runtime | LULUS | Production Go handler + PostgREST16.5 + PostgreSQL nyata, provider/identitas fixture lokal: opt-out kosong;206final dalam5halaman, seq2…411 dan partial tidak tersimpan;401/404 akun asing; delete aktif tidak menghentikan capture/menumbuhkan kembali teks; repeat delete sukses. Binary terakhir diulang setelah seluruh backend review fixes dan lulus |
| Popup runtime + file unduhan | LULUS LOKAL TERKONTROL | Chromium150 menjalankan React/worker aktual dengan bridge Chrome/provider/identitas terkontrol: checkbox awal false, opt-in start/stop, next capture default-off, timeline50→100, unduhan JSON31.141byte berisi206final/through_seq411/complete=true, delete menjadi0baris, tanpa page exception |
| Supabase target0004/0005 | BELUM DITERAPKAN SESI INI | Tidak ada cloud mutation; operator harus apply dan memeriksa grant/job target |
| Extension unpacked/tabCapture/AudioWorklet + provider nyata | BELUM DIUJI SESI INI | UI smoke tidak membuktikan permission/native media lifecycle; F4 provider live sebelumnya tetap bukti historis terpisah |

Tidak ada kredensial eksternal dibaca untuk smoke, request Deepgram/Sumopod berbayar, penyimpanan audio manusia, atau commit/reset otomatis.

### Regresi yang ditemukan saat verifikasi

- Fixture Go baru awalnya memakai key dummy yang tidak lolos validasi; writer gagal sebelum test gate, membuat suite timeout. Yang diperbaiki hanya fixture publishable key, bukan validasi produksi.
- Browser menemukan effect React memakai `active` tanpa deklarasi; setelah diperbaiki popup dirender tanpa exception.
- Review frontend menemukan cleanup auth, delete/reselection, reciprocal terminal read/export, dan pagination-reset races; semuanya direproduksi gagal sebelum fix. Re-review menemukan pending list loading yang kemudian direproduksi/fix juga; suite147 dan scoped review lulus.
- Review backend menemukan writer premature Finish, snapshot lengkap palsu, dan stale stop metadata. Seluruh varian direproduksi gagal sebelum fix; regresi finalisasi/provider-open/deletion dan suite race lulus sesudah fix. Scoped review lanjutan menemukan refresh metadata tambahan yang dapat menggagalkan stop yang sudah diakui; varian timeout dan missing-row direproduksi lalu diperbaiki: HTTP200/stopped dipertahankan, durabilitas tanpa metadata terbaru tetap incomplete, late final tetap masuk. Full race/vet/build lulus lagi; source-only re-review terakhir menyatakan semua temuan teratasi dan tidak menemukan masalah konsekuensial baru (confidence0.98).

## Aktivasi dan pemeriksaan operator

1. Hentikan capture aktif sebelum upgrade/reload agar finalisasi dan writer drain selesai.
2. Terapkan0004 setelah0003, aktifkan Supabase Cron, terapkan0005 sebagai admin. Jangan mengaktifkan F6 bila scheduler belum berhasil. [Panduan migration dan query job](../../migration/README.md#transkrip-final-dan-retensi-f6).
3. Build/restart backend, build extension dan reload `extension/dist`. Tidak menambah izin extension, dependency Go/npm atau service Compose baru; Supabase Cron adalah prasyarat operasional terpisah.
4. Pada Chrome unpacked: login, buka meeting supported, coba capture tanpa centang lalu dengan centang; tutup/buka popup, berhenti, baca run tersimpan dan ekspor. Pastikan capture berikutnya tidak otomatis opt-in.
5. Uji delete saat capture berjalan, akun kedua tidak dapat membaca isi, dan logout/credential expiry tidak meninggalkan media aktif. Verifikasi job target aktif/sukses tanpa memasukkan teks transkrip ke query/log.
6. Rollback aplikasi tidak mengharuskan DROP schema/data atau mematikan job retensi. Pertahankan retensi untuk arsip yang sudah dibuat; tidak ada rollback destruktif otomatis.

## Obsidian

Vault tujuan `Obsidian Vault`, folder `Proyek/LiveTranslate`. Local REST API tidak terhubung saat pembacaan proyek; belum ada write/read-back atau perubahan baseline sinkronisasi. Desain, plan, roadmap, panduan dan status berada dalam antrean `docs/obsidian-pending/pending.md` untuk sinkronisasi tanpa menimpa edit vault.
