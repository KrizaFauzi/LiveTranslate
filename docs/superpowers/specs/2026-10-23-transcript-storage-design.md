# F6 — Penyimpanan transkrip dan timeline

> **HISTORICAL — Superseded by 2026-10-24 execution planning (02-lifecycle-archive.md).** This spec describes opt-in final transcript storage with 30-day retention and pg_cron purge. Current design: post-meeting consent, permanent archives, staging TTL 24h. See `docs/superpowers/plans/2026-10-24-details/02-lifecycle-archive.md` for current design. Contents preserved for history; do not execute.

Tanggal: 2026-10-23.
Status: DISETUJUI pengguna 2026-10-23, termasuk kegagalan penyimpanan yang tidak menghentikan transkripsi, anchor retensi, dan scheduler Supabase. Implementasi/verifikasi mengikuti plan; persetujuan bukan bukti fitur selesai.
Proyek: LiveTranslate.
Sumber lokal: `docs/superpowers/specs/2026-10-23-transcript-storage-design.md`.
Dasar: `docs/implementation/feature-roadmap.md`, implementasi F3/F4, dan keputusan pengguna pada sesi kelanjutan roadmap.

## 1. Intent dan keputusan pengguna

Pengguna dapat membaca kembali transkrip meeting yang secara eksplisit dipilih untuk disimpan. Capture dan transkripsi live yang sudah tersedia tetap dapat digunakan tanpa penyimpanan permanen.

Keputusan pengguna:

- Sumber audio tetap tab meeting saja, bukan mikrofon.
- Penyimpanan bersifat opt-in per capture/run; default mati.
- Hanya segmen final yang disimpan; audio mentah tidak disimpan.
- Retensi transkrip tersimpan adalah 30 hari.
- Pemilihan run tersimpan dan timeline memakai popup yang sudah ada, tanpa halaman extension baru.
- Pengguna dapat menghapus transkrip per run dan mengekspor JSON. Identitas meeting dan metadata capture tidak ikut dihapus.

Bagian lain di bawah berasal dari usulan teknis penulis desain. Pengguna menyetujui dokumen lengkap ini serta plan implementasinya pada 2026-10-23; pilihan awal dari penulis tetap dibedakan dari requirement yang mula-mula dinyatakan pengguna.

## 2. Scope, non-goals, dan anti-goals

Fitur: simpan final server-side untuk run opt-in; baca run/segmen tersimpan dari database; tampilkan timestamp/timeline di popup; hapus; ekspor; enforce expiry dan jalankan penghapusan otomatis; bukti ownership/RLS, idempotensi dan batas lifecycle.

Tidak termasuk: terjemahan atau tabel translations, AI/ringkasan/RAG, daftar history lintas semua meeting, audio tersimpan, diarization, reconnect Deepgram, migrasi transkrip lama dari browser, penyimpanan offline permanen, crash-proof queue, dan perubahan lifecycle capture F3 yang tidak diperlukan integrasi F6.

Fitur: simpan final server-side untuk run opt-in; baca run/segmen tersimpan dari database; tampilkan timestamp/timeline di popup; hapus; ekspor; enforce expiry dan jalankan penghapusan otomatis; bukti ownership/RLS, idempotensi dan batas lifecycle.

**Catatan:** Desain ini digantikan. Desain baru: consent pasca-meeting, arsip permanen tanpa expiry, staging 24-jam, paket arsip mencakup catatan, Q/A bilingual, dokumen, kutipan. Lihat planning 2026-10-24 detail 02.

- Jangan menyimpan tanpa checkbox opt-in, termasuk mengingat opt-in untuk capture berikutnya.
- Jangan memakai cache 100 update backend atau 200 final extension sebagai sumber history/ekspor.
- Jangan menganggap event SSE diterima atau capture berhenti sebagai bukti penyimpanan sukses.
- Jangan menghidupkan kembali transkrip yang sudah dihapus atau expired.
- Jangan menjanjikan semua final selamat saat backend/browser crash sebelum database mengakui write.

## 3. Batas stack dan fakta integrasi

React + Vite JavaScript MV3; Go standard library; Supabase Auth dan Data API dengan publishable key + JWT pengguna. Tidak menambah dependency runtime Go/JS, service, queue eksternal, service-role key, atau refresh token backend.

F3 dan F4 sudah ada. `meeting_id` tetap identitas link; `meeting_runs.id` adalah identitas satu capture.

`transcribe.Session` menetapkan `Segment.Seq` untuk partial dan final, sehingga seq final boleh berlubang. Pertahankan seq tersebut. Backlog/subscriber SSE dibatasi dan subscriber dapat kehilangan update ketika penuh; jangan gunakan `Subscribe` sebagai sumber penyimpanan.

Integrasi persistence harus dipasang ketika sesi dibuat, sebelum goroutine provider mulai mengonsumsi hasil. `audio` tetap pemilik kredensial/repository; `transcribe` menerima sink final tanpa mengetahui Supabase/JWT. Migrate seluruh caller signature yang berubah, tanpa shim.

## 4. Alur dan consent

1. Popup menampilkan checkbox tidak tercentang: `Simpan transkrip final selama 30 hari` dengan keterangan `Audio tidak disimpan. Anda dapat menghapus atau mengekspor transkrip.`
2. `audio.start` membawa hanya boolean `save_transcript`; worker tetap memilih meeting/tab melalui alur yang ada. Nilai selain boolean ditolak; field tidak ada berarti false.
3. Server membuat run dengan pilihan penyimpanan yang tidak dapat dinaikkan dari false ke true setelah run dibuat. Semua run lama tetap default disabled; tidak ada backfill dari memori browser.
4. Saat start diajukan, checkbox berikutnya kembali false. Saat capture berjalan, tampilkan pilihan yang dikonfirmasi server untuk run itu, bukan nilai checkbox berikutnya. Popup ditutup/dibuka atau akun/meeting berubah tidak mengaktifkan simpan untuk run berikutnya.
5. Sink backend hanya mengirim final nonkosong ke writer run opt-in. Partial tetap lewat SSE/memori saja.
6. Penyimpanan tidak bergantung pada popup, SSE subscriber, atau service worker yang tetap hidup.

## 5. Model data dan otorisasi

Migration `0004_transcript_segments.sql` setelah 0003:

Tambahkan metadata pada `meeting_runs`:

- `save_transcript boolean NOT NULL DEFAULT false`: consent awal, immutable.
- `transcript_state`: `disabled`, `saving`, `saved`, `failed`, atau `deleted`.
- `transcript_expires_at timestamptz`: untuk run opt-in, dihitung database sebagai `started_at + interval '30 days'`; null untuk opt-out. Immutable, sehingga update capture tidak memperpanjang retensi.

`expired` adalah keadaan turunan bila waktu database aktual >= expiry, bukan klaim bahwa capture metadata sudah dihapus. Pilihan anchor `started_at` adalah keputusan teknis penulis: selalu bounded bahkan bila browser crash dan run tidak pernah ditutup.

Tabel `public.transcript_segments`:

- `id uuid` primary key; `user_id uuid` FK auth.users; `meeting_id uuid` dan `run_id uuid` FK yang konsisten dengan run; timestamp `created_at`.
- `seq integer > 0`, `text text`, `start double precision >= 0`, `duration double precision >= 0`, `language text`.
- Unique `(run_id, seq)`; indeks baca `(run_id, seq)`.
- Hanya final; tidak ada kolom partial/audio/token/provider payload. Isi teks final tidak diubah setelah insert.
- Hapus akun/run memakai FK cascade seperti tabel meeting yang ada. Endpoint F6 sendiri tidak menghapus metadata run/meeting.

Database wajib memvalidasi user_id dan meeting_id terhadap parent run; menerima ID run saja dari pemanggil tidak membuktikan ownership. RLS owner-only untuk baca/tulis. Read/write juga mensyaratkan consent, belum deleted, dan belum expired. Anon tidak mendapat hak akses. Semua RPC aplikasi SECURITY INVOKER dengan search_path tetap dan grants minimum, seperti fungsi audio saat ini.

RPC append-final dan delete-transcript mengunci parent run yang sama (`FOR UPDATE`) sebelum mengubah isi. Insert trigger/constraint menerapkan pemeriksaan consent, ownership, expiry, dan lock juga pada insert Data API langsung, bukan hanya jalur backend. Setelah memperoleh lock, periksa expiry memakai `clock_timestamp()`; `now()`/transaction_timestamp yang membeku sebelum menunggu lock tidak cukup. Jangan beri UPDATE teks final. Duplikat identik pada `(run_id, seq)` bersifat idempoten; konten berbeda pada identitas yang sama ditolak, tidak menimpa final.

Penghapusan atomik menetapkan transcript_state=deleted dan menghapus semua segmen; consent awal tetap sebagai jejak keputusan, tetapi state deleted menolak write selanjutnya. Invariant ini berlaku pada seluruh mutation path yang diberi grants, termasuk DELETE segmen dan PATCH state melalui Data API langsung: operasi tersebut harus menjalankan barrier+purge yang sama secara transaksional atau ditolak. Tidak boleh ada DELETE sukses yang menyisakan run writable, atau state deleted dengan text yang hanya disembunyikan. Gunakan database guards/trigger untuk mengikat penghapusan dengan transisi state; fungsi SECURITY INVOKER tidak menghilangkan hak dasar caller, sehingga mengamankan RPC saja tidak cukup. Direct delete yang tidak dapat menentukan parent run (misalnya tidak ada row cocok) boleh ditolak; idempotensi penghapusan aplikasi disediakan RPC yang memiliki run UUID eksplisit.

Transisi deleted/disabled/expired tidak dapat diaktifkan kembali oleh caller aplikasi. Akses run pemilik tetap tersedia; metadata capture dan link tidak dihapus. Jalur maintenance admin boleh menghapus isi expired secara fisik dan FK cascade tetap bekerja; privilege admin bukan privilege aplikasi.

## 6. Persistence backend dan batas kegagalan

Writer terpisah per run menerima final langsung dari jalur publication ber-seq, bukan subscriber SSE. Queue hanya dalam memori, maksimal 128 final atau 2 MiB teks UTF-8 yang menunggu, mana yang tercapai lebih dahulu. Tidak ada I/O database di bawah mutex sesi. Satu writer per run mempertahankan urutan; setiap request Data API punya deadline 10 detik.

Status:

- Opt-out: disabled, tidak pernah menulis teks.
- Opt-in berjalan: saving.
- Provider/session sudah selesai dan semua final yang diterima backend diakui database: saved.
- Write gagal, timeout, auth gagal, overflow atau drain tidak selesai: failed/incomplete, bukan saved.
- Penghapusan pengguna: deleted; writer berhenti menyimpan dan membuang pending setelah database memastikan barrier delete.
- Expiry: expired; write tidak dilanjutkan.

Keputusan kegagalan yang diajukan penulis: **penyimpanan gagal tidak menghentikan capture/transkripsi live; popup menampilkan peringatan bahwa transkrip tersimpan tidak lengkap**. Setelah kegagalan pertama, writer tidak mengklaim sukses atau mencoba menyimpan sisa run secara diam-diam. Tidak ada retry/reconnect otomatis atau persistent outbox pada F6. Segmen yang sudah diakui tetap dapat dibaca/diekspor sebagai hasil tidak lengkap. Tidak ada klaim crash-proof durability sebelum acknowledgment database.

Jika database tidak dapat menerima patch status failed, state tetap saving/pending, bukan saved. API menggabungkan status writer live bila masih ada; sesudah restart, state saving tanpa writer dianggap incomplete, tidak dianggap selesai. Jangan mengubah counter/run capture menjadi saved untuk menutup kondisi ini.

Sink menerima final sebelum fan-out SSE; enqueue kegagalan ditangani sebagai storage failure tanpa merusak transkripsi. Persistence terpasang sebelum provider supervision dimulai, sehingga final awal tidak terlewat.

### Kredensial dan finalisasi

Writer memakai JWT pemilik terkini yang sudah diverifikasi dan lolos ownership check pada create/chunk/stop; seed sebelum sesi dibuka. Token hanya di memori dan tidak masuk log/database/response popup. Request lama tidak boleh mengganti credential baru: gunakan ticket request monoton yang ditetapkan sebelum verifikasi dan hanya terima hasil terbaru untuk owner yang sama. Jangan decode claim token sebagai pengganti verifikasi.

Tidak menambah refresh token backend atau endpoint recovery; jika token kedaluwarsa tanpa request pemilik baru dan write gagal, tampilkan kegagalan penyimpanan sesuai aturan di atas. Refresh chunk yang sudah ada memperbarui credential writer setelah ownership check; stop juga memperbaruinya sebelum finalisasi.

Stop tetap memfinalisasi provider dan mempertahankan dokumen offscreen sampai transkrip berakhir sesuai F4. Final yang diterima saat/selepas stop tetap boleh diinsert walaupun metadata capture sudah stopped/failed, selama consent/expiry/delete mengizinkan. Anggaran task background finalisasi+writer drain adalah 30 detik sejak stop/provider termination; request database tetap maksimum 10 detik dan mengikuti sisa anggaran. Stop HTTP mempertahankan batas respons F4 dan dapat menjawab capture stopped + storage pending/failed, tidak boleh menjawab saved sebelum drain diakui. Credential dan writer tidak dibuang sebelum task finalisasi/drain selesai atau batas waktunya habis; pada deadline, cancel I/O dan tandai incomplete. Provider gagal tetap menyelesaikan final yang sudah diterima bila writer masih sehat.

## 7. API F6

Semua route memakai Bearer JWT, verifikasi ownership melalui Data API/RLS, no-store, error generik tanpa isi transkrip/token. Run hilang/asing -> 404. Pemilik dengan isi opt-out/deleted/expired -> metadata availability eksplisit; text tidak dikembalikan.

- `POST /api/audio/runs`: kontrak lama ditambah boolean optional `save_transcript` default false; response run memuat metadata storage. Migrate parser yang saat ini mensyaratkan dua field persis.
- `GET /api/audio/runs?meeting_id=<uuid>&limit=<n>&cursor=<opaque>`: daftar run opt-in yang belum expired/deleted untuk meeting aktif milik pengguna. Urutan `(started_at DESC, id DESC)` stabil; default 20, maksimum 100; empty speech run tetap boleh tampil. Cursor terikat owner/meeting dan posisi, bukan otorisasi tersendiri.
- `GET /api/audio/runs/{id}/segments?after_seq=<n>&through_seq=<n>&limit=<n>`: metadata run/availability + final berurutan + next cursor; default 50, maksimum 100. Request pertama menetapkan `through_seq` sebagai seq final maksimum yang sudah disimpan, sehingga pembacaan/ekspor aktif adalah snapshot. Halaman berikut mempertahankan batas itu. Metadata storage dan run berbeda field, tidak mengubah response SSE.
- `DELETE /api/audio/runs/{id}/segments`: idempoten, atomik delete+disable future writes; response metadata run/availability tanpa isi teks. Repeat pemilik tetap sukses. Delete hanya transkrip, bukan run.

Tidak ada endpoint append teks khusus extension: jalur aplikasi menyimpan final yang dibuat server. Append/list pada katalog G-05 diwujudkan sebagai RPC append + HTTP list. **Ini bukan jaminan provenance provider pada level database:** backend memakai JWT pemilik yang sama dengan authenticated caller, sehingga pemilik dapat memanggil RPC atau Data API langsung sesuai grants. Ownership/consent/expiry/delete dijamin database; keaslian teks provider dan status final dijamin jalur aplikasi saja. Jangan memberi label transkrip tersimpan sebagai bukti percakapan yang terverifikasi atau mengklaim RPC hanya dapat dipanggil backend. Tidak membuat route lama rencana `/api/meetings/{id}/segments`, yang mengaburkan occurrence.

Ekspor memakai pagination read yang sama dari worker, bukan endpoint kedua yang mengimplementasikan aturan data berbeda. Seluruh halaman sampai snapshot through_seq harus selesai sebelum file dibuat. Tidak hanya halaman tampak atau 200 final terakhir. Batas response yang ditetapkan implementasi harus gagal eksplisit jika dilampaui, bukan memotong teks atau mengeluarkan JSON parsial seolah lengkap.

## 8. Popup, worker, dan ekspor

Checkbox di kartu capture; pilihan tersimpan server ditampilkan selama run berjalan. Timeline tersimpan di bagian terpisah dari transcript live dengan selector run untuk meeting aktif dan pilihan Live. Tampilkan tanggal capture, status availability/complete, expiry, timestamp relatif `start` + teks segmen, empty/loading/error state. Pergantian pilihan tidak mengubah capture atau cache live.

Popup RPC hanya untuk sender extension tanpa tab seperti saat ini:

- `audio.start` input `{save_transcript:boolean}`.
- `transcript.runs.list` input `{meeting_id,cursor?}`.
- `transcript.run.read` input `{meeting_id,run_id,after_seq?,through_seq?,limit?}`.
- `transcript.run.delete` input `{meeting_id,run_id}`.

Worker tidak memberikan token ke popup, mengambil credential sendiri, membatasi ID ke account/meeting yang relevan, mengecek kembali account setelah setiap await/refresh. Input meeting_id tambahan diverifikasi terhadap run; ID dari UI bukan bukti authorization.

Ekspor: popup meminta halaman run melalui worker, mengumpulkan snapshot JSON lalu membuat UTF-8 Blob `application/json` dan anchor download; nama file dari run UUID, bukan text/URL meeting. Schema:

`{schema_version:1, run:{id,meeting_id,started_at,ended_at}, storage:{state,expires_at,complete,through_seq}, segments:[{seq,text,start,duration,language}]}`.

State complete true hanya jika saved; ekspor saat aktif/failed ditandai snapshot/incomplete. Tidak ada credential, provider body, URL sensitif, audio, atau partial. Revoke object URL setelah digunakan/unmount. Tidak menambah downloads permission tanpa bukti bahwa jalur browser membutuhkannya. Jika ekspor gagal di tengah, discard hasil sementara dan tidak membuat file sukses.

Paginasi dan ekspor memakai generation/ticket berdasarkan owner+meeting+selected run; account change/logout/meeting change/selection baru membatalkan atau mengabaikan response lama dan membuang data lokal. Cek ticket sebelum memulai download. Delete membatalkan export/read pilihan itu dan membersihkan tampilan saved; live text boleh tetap ada, dengan label bahwa itu hanya sesi sementara dan bukan arsip. Salinan JSON yang sudah diunduh pengguna berada di luar retensi server.

Jangan menyimpan teks ke chrome.storage.local atau outbox meeting. `storage.session` tetap untuk live display bounded. Saat menyentuh state F6, bind owner dan run dengan jelas; bersihkan transient live/saved state pada logout/account change agar akun berikutnya tidak melihat data lama. Offscreen progress/error/token untuk suatu run tidak boleh diterapkan ke owner/run berikutnya; tambahkan identity checks pada jalur yang diperlukan F6, tanpa redesign capture.

## 9. Retensi 30 hari yang benar-benar dijalankan

Database menghitung expiry, bukan jam client. Read/list/export/write ditolak atau menghilangkan isi expired ketika `clock_timestamp() >= transcript_expires_at`, termasuk akses langsung Data API/RLS. Khusus write, pemeriksaan wall-clock wajib diulang setelah lock parent run diperoleh agar transaksi yang mulai sebelum expiry tetapi menunggu lock tidak mendapat izin lama. Cron bukan lapisan otorisasi.

Migration operasional terpisah `0005_transcript_retention_cron.sql` memasang job bernama tetap pada Supabase pg_cron yang berjalan setiap menit dan DELETE segmen expired dengan join metadata run, tanpa menghapus metadata meeting/capture. Re-apply memperbarui job bernama yang sama, bukan menambah duplikat. SQL maintenance berjalan sebagai admin database yang memasang job; role aplikasi tidak memperoleh hak lintas pengguna. Tidak ada service-role secret dalam aplikasi.

Ini keputusan deployment penulis yang harus disetujui: memakai scheduler bawaan project Supabase, bukan service baru. pg_cron harus diaktifkan/tersedia ketika migration operasional diterapkan. Bila tidak tersedia, migration gagal jelas; jangan melaporkan retensi siap atau memasang fallback no-op. Pisahkan test skema/RLS PostgreSQL standar dari pemeriksaan scheduler pg_cron.

Dalam kondisi scheduler sehat, teks tidak dapat dibaca sesudah expiry dan dihapus fisik pada putaran menit berikutnya. Scheduler downtime dapat menunda physical purge; dokumentasikan pengecekan job/job_run_details. Jangan menjanjikan penghapusan backup/PITR/provider logs yang dikelola Supabase/Deepgram; kebijakan eksternal tersebut tidak diubah F6. SQL job tidak memasukkan isi transkrip ke command/log.

## 10. Acceptance dan verifikasi

1. Default/false consent menghasilkan nol write teks; true menyimpan final saja, termasuk saat popup/SSE tidak terbuka.
2. Lebih dari 200 final dan backlog SSE terlampaui tetap dapat dibaca dan diekspor lengkap dari database. Seq final mempertahankan gap partial; duplikat identik idempoten, konflik final ditolak.
3. Dua akun tidak dapat list/read/append/delete/export isi satu sama lain melalui backend maupun Data API/RLS; parent run/meeting/owner mismatch ditolak.
4. Popup close/reopen, worker restart, run baru, dan akun berganti tidak mengubah consent run atau membocorkan data lama. Opt-in tidak terbawa ke capture berikutnya.
5. Final saat stop/provider termination masuk writer sebelum cleanup; saved baru setelah drain acknowledged. JWT chunk/stop yang sudah diverifikasi memperbarui writer owner; request lama/akun lain tidak dapat menggantinya.
6. Slow/unavailable DB, token expired, queue penuh, dan backend restart menghasilkan pending/failed/incomplete yang terlihat, bukan klaim saved; capture live tidak sengaja diganti menjadi storage failure/transcript success.
7. Delete berbalapan dengan write yang sudah dikirim/queued tidak menghidupkan kembali isi; capture live dan metadata meeting/run tetap ada. Delete idempoten.
8. Boundary expiry memakai waktu database: sebelum 30 hari boleh, tepat expiry tidak boleh read/write/export, dan fungsi purge benar-benar menghapus baris expired tanpa menghapus run atau baris belum expired. Pemasangan cron idempoten diperiksa terpisah.
9. Timeline dan JSON export menangani multi-page, empty speech, active snapshot, failed/incomplete result, account/selection change, expiry atau delete di tengah export; tidak mengunduh hasil parsial sebagai sukses.
10. Smoke runtime wajib: jalankan backend lokal dengan Data API/provider terkontrol tanpa biaya untuk membuktikan create opt-in -> emitted final -> durable read -> JSON data -> delete/expiry. PostgreSQL throwaway membuktikan transaksi/RLS/purge. Browser aktual membuktikan popup checkbox/timeline/download bila runtime tersedia; bila tidak, laporkan batas visual/manual, jangan naikkan status validasi Chrome.

Permanent regression tests hanya untuk invariants/boundary/race di atas, mengikuti Node test dan Go httptest/harness SQL yang ada. Tidak ada test source-text/copy/wiring/no-throw. Jalankan suite extension/backend dan build, contract auth serta harness migrasi yang terdampak. Tidak ada request Deepgram/Sumopod berbayar atau mutasi project cloud otomatis dalam test biasa.

## 11. Dokumen dan pelaksanaan

Sesudah desain disetujui: buat implementation plan memakai writing-plans, implementasikan dengan TDD, jalankan smoke, update README/migration guide, status F6, dan roadmap dengan bukti aktual. F4/Chrome yang belum tervalidasi tetap ditulis sebagai batas, bukan diselesaikan oleh fake smoke. Jangan menandai F6 selesai dari keberadaan source.

Simpan desain/plan/status ke Obsidian sesuai mapping proyek; selama Local REST API tidak terhubung gunakan antrean lokal dan jangan ubah baseline seolah sudah tersinkron.

## 12. Review desain

Builder check independen tidak menemukan pertanyaan produk tambahan yang penting. Review privacy/transaction menemukan tiga kontrak yang diperjelas sebelum persetujuan: barrier delete pada seluruh jalur Data API, batas trust/provenance RPC memakai JWT pemilik, dan pemeriksaan expiry wall-clock setelah lock. Review ini pemeriksaan dokumen/source saja, bukan bukti test atau implementasi.

## Referensi

- [Supabase Cron](https://supabase.com/docs/guides/cron)
- [Supabase Cron quickstart](https://supabase.com/docs/guides/cron/quickstart)
- [Aktivasi pg_cron](https://supabase.com/docs/guides/cron/install)

Dokumentasi Supabase juga diperiksa melalui Context7 pada sesi ini. Temuan source F3/F4 berasal dari pemeriksaan read-only, bukan hasil test baru.
