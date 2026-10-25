# LiveTranslate — PRD dan desain penyelarasan produk

Tanggal: 2026-10-24. Status: DISETUJUI pengguna sebagai PRD/desain, termasuk rekomendasi yang dipaparkan. Bukan plan eksekusi lengkap. Pengguna kembali menegaskan **tidak mengizinkan implementasi**; persetujuan hanya untuk dokumen/desain.

Dokumen ini menyatukan kebutuhan pengguna karena PRD yang dirujuk plan lama belum ditemukan lokal dan Obsidian tidak terhubung. Tidak mengklaim menggantikan isi PRD eksternal yang belum terbaca. Bukti implementasi tetap di [roadmap](../../implementation/feature-roadmap.md); percakapan keputusan dirangkum pada [catatan brainstorming](../../obsidian-pending/2026-10-24-planning-realignment-brainstorm.md).

## 1. Intent dan batas

Pendamping meeting browser untuk tim sangat kecil, mungkin kurang dari tiga pengguna: memahami percakapan peserta lain melalui terjemahan Indonesia, memetakan pertanyaan, dan membantu menjawab berdasarkan dokumen khusus meeting secara bilingual dengan sumber yang dapat diperiksa.

Sesi ini hanya menghasilkan desain dan planning. Jangan menjalankan migrasi, memodifikasi kode, mengubah provider, membeli layanan, atau menghapus data. Repository lama `live_translate/` bila ditemukan harus dipertahankan; dokumen sebelumnya mencatat tidak ditemukan.

Label **Keputusan pengguna** berarti kebutuhan yang dinyatakan/disetujui langsung. Label **Rekomendasi** mempertahankan asal pilihan teknis penulis; rekomendasi dalam dokumen ini kemudian disetujui pengguna sebagai desain lengkap. Persetujuan desain bukan bukti kelayakan provider, hasil benchmark, atau izin implementasi.

## 2. Kebutuhan yang sudah diputuskan

| ID PRD | Keputusan pengguna | Bukti penerimaan produk yang diperlukan nanti |
| --- | --- | --- |
| PRD-01 | Deteksi meeting otomatis dan tawarkan koneksi ke LiveTranslate | Tawaran muncul pada meeting yang didukung tanpa pengguna membuka popup secara manual; menolak tidak memulai capture |
| PRD-02 | Audio peserta lain dari tab saja; tanpa akses mikrofon sendiri | Tidak meminta izin mikrofon; pemrosesan memakai audio tab yang disetujui |
| PRD-03 | Bahasa sumber beragam, hasil terjemahan Indonesia | Bahasa yang diumumkan didukung diuji; ketidakdukungan tidak ditutupi terjemahan palsu |
| PRD-04 | Catatan terjemahan dapat dibaca selama meeting | Pembaruan berurutan dan terikat meeting yang benar |
| PRD-05 | Semua pertanyaan peserta lain dipetakan, siapa pun penerimanya | Tidak menyaring nama panggilan atau menebak penerima; beberapa pertanyaan terpisah punya item masing-masing |
| PRD-06 | Jawaban hanya dari dokumen khusus meeting | Tidak menggunakan dokumen meeting lain atau pengetahuan umum sebagai pengganti bukti |
| PRD-07 | Sumber berupa PDF, DOCX, spreadsheet | Ekstraksi yang didukung mempunyai locator sumber dan kegagalan eksplisit |
| PRD-08 | Daftar bernomor pertanyaan → jawaban → bagian dokumen | Jawaban/citation tetap terikat item yang sama walau hasil AI datang tidak berurutan |
| PRD-09 | Tanpa bukti dokumen, jawaban kosong atau `-` | Rekomendasi tampilan konsisten `-`; tidak mengarang jawaban/kutipan |
| PRD-10 | Jawaban Indonesia dan Inggris baku/grammatikal | Makna dan bukti konsisten di kedua bahasa; angka, satuan, negasi dan kepastian tidak berubah |
| PRD-11 | Sesudah meeting tanyakan apakah hasil disimpan | Tidak menganggap izin capture sebagai izin arsip permanen |
| PRD-12 | Jika disimpan, sertakan catatan, pertanyaan/jawaban, dokumen dan kutipan | Arsip lengkap dapat dibuka kembali beserta sumbernya |
| PRD-13 | Arsip disimpan selamanya | Tidak ada expiry atau purge berdasarkan umur untuk arsip disetujui |
| PRD-14 | Setiap anggota hanya boleh melihat miliknya sendiri | Akses lintas akun ditolak pada metadata, file, pencarian, kutipan, ekspor dan arsip |
| PRD-15 | Tim terbatas, mungkin kurang dari tiga orang | Desain tidak membutuhkan infrastruktur layanan publik skala besar |

Audio mentah tidak menjadi arsip. Pemolesan jawaban beberapa iterasi adalah kemungkinan yang diajukan pengguna, bukan keharusan framework multi-agent. Tidak ada izin mengirim chat atau membacakan jawaban ke meeting atas nama pengguna.

## 3. Baseline yang dipertahankan dan yang direvisi

Baseline dokumen: React/JavaScript MV3, Go, Supabase Auth/Data API/RLS, Deepgram, Sumopod. Satu backend Compose. Auth, meeting URL, capture, transkripsi dan storage F6 mempunyai implementasi dengan batas validasi yang tercatat; jangan membangunnya ulang dari nol atau menyebutnya siap produksi.

Revisi P0:

1. Consent F6 sebelum capture diganti keputusan arsip setelah meeting. Field `save_transcript` lama immutable; tidak cukup membalik boolean pada akhir run.
2. Retensi30hari diganti arsip tanpa expiry. Migration/RLS/job purge harus membedakan staging dan arsip; jangan mematikan semua cleanup.
3. Identitas URL meeting bukan occurrence. Dokumen dan konteks harus terisolasi per pertemuan aktual, termasuk URL yang dipakai lagi besok.
4. Deteksi URL tidak membuktikan pengguna sudah bergabung. Tawaran otomatis dan pengakhiran harus punya kontrak lifecycle tersendiri.
5. Retrieval dokumen/citation menjadi kebutuhan inti, bukan fitur opsional sesudah sliding context gagal.
6. Plan awal pgx dan auth Node/Drizzle sudah bertentangan dengan arsitektur terkini. Dalam paket plan baru, labeli historis dengan pengganti; pertahankan bukti, jangan menghapus riwayat keputusan.

## 4. Arsitektur rekomendasi

**Satu backend Go modular**, bukan microservice per fitur. Alasan: tim kecil, biaya operasi rendah, tidak menambah hop jaringan pada audio, mudah menelusuri kegagalan. Ekstraksi dokumen boleh dipisahkan ke proses terbatas resource untuk isolasi parser, bukan otomatis menjadi service jaringan baru.

| Modul | Kepemilikan | Larangan coupling |
| --- | --- | --- |
| Identitas/akses | Verifikasi sesi, akun yang diizinkan, ownership | Tidak mempercayai user_id dari client |
| Meeting | Room identity, occurrence, lifecycle, mapping tab | Tidak mengambil keputusan consent untuk pengguna |
| Capture/transkripsi | Audio streaming, seq/timestamp, partial/final, gap | Tidak menunggu AI/retrieval/storage untuk mengalirkan audio |
| Terjemahan | Segmen sumber → Indonesia, revisi dan status | Tidak mengubah transkrip asli |
| Dokumen | Upload, validasi, ekstraksi, versi, locator | Tidak menjalankan instruksi/macro dalam file |
| Retrieval | Pencarian bukti dalam owner+occurrence | Tidak mengakses pustaka global/lintas meeting |
| Assistance | Pertanyaan, jawaban bilingual, bukti, revisi terbatas | Tidak menebak fakta ketika sumber tidak mendukung |
| Arsip | Consent, commit paket, manifest, baca/hapus/ekspor | Tidak mengklaim lengkap sebelum semua objek tersedia |

Entry point hanya menyusun modul. Provider adapter tidak menentukan kebijakan produk. DTO domain terpisah dari respons provider; cancellation/deadline mengikuti request/job. Hindari interface generik yang tidak dibutuhkan. Jangan menambah Redis, Kubernetes, broker, vector DB eksternal atau framework agent tanpa bukti kebutuhan.

Supabase tetap kandidat storage metadata dan private object storage. Pemilihan parser/OCR/retrieval/model harus didukung dokumentasi resmi, kemampuan akun dan evaluasi sebelum dimasukkan sebagai langkah implementasi pasti; belum divalidasi pada sesi ini. Penambahan dependency runtime memerlukan desain yang disetujui.

## 5. Lifecycle meeting dan sumber konteks

Rekomendasi:

- `meeting_id` lama tetap identitas ruang/link. Tambahkan konsep occurrence milik satu pengguna untuk satu pertemuan, dengan satu atau lebih capture run. Jangan merename ID lama secara sembarang.
- Occurrence dapat mempunyai dokumen sebelum capture; berhenti capture tidak otomatis berarti meeting selesai. Lifecycle occurrence terpisah dari run transport.
- Tawaran koneksi tidak mengirim audio sebelum pengguna menerima. Penolakan tidak memicu prompt berulang pada event URL yang sama.
- Deteksi masuk/keluar per provider dipisahkan dari normalisasi URL. Jika joined-state tidak pasti, jangan mengklaim pasti; sediakan tindakan eksplisit menghubungkan/mengakhiri.
- Tab tertutup/navigasi keluar perlu mengakhiri capture, tetapi keputusan arsip tetap tersedia di extension. Menutup popup tidak mengakhiri meeting.
- Jangan menggantungkan deteksi akhir pada audio sunyi. Jangan mengarang waktu akhir setelah crash.
- Pertahankan satu capture aktif per akun pada rilis awal sebagai rekomendasi batas operasional; tolak start kedua secara eksplisit.
- Logout/account change membersihkan token dan tampilan sementara; tugas akun lama tidak diteruskan sebagai akun baru.

Audio tab adalah campuran keluaran meeting. Tanpa pemisahan sumber, suara pengguna yang dipantulkan peserta lain dapat ikut terdengar; jangan menjanjikan penyaringan speaker sempurna. Mikrofon lokal tetap tidak diakses.

## 6. Catatan sementara dan arsip permanen

Rekomendasi yang memerlukan persetujuan sebagai bagian desain:

- Catatan sementara diproses/disimpan secara privat selama sesi untuk menjamin semua halaman tersedia, bukan hanya cache200final. Lokasi sementara dan pengiriman ke provider harus dijelaskan sebelum koneksi.
- Gunakan staging privat di backend/storage, bukan arsip tersembunyi. Staging meliputi catatan, dokumen, hasil ekstraksi dan jawaban; tidak meliputi audio mentah.
- Setelah akhir meeting, state `awaiting_decision`. Usulkan batas24jam untuk kesempatan memilih, termasuk setelah browser ditutup; tidak memilih sampai batas itu berarti hapus staging, bukan auto-save. Ini **bukan keputusan pengguna** dan tidak berlaku pada arsip yang sudah disimpan.
- Jangan memperpanjang staging tanpa batas dengan heartbeat palsu. Runtime harus membatasi meeting aktif dan mendeteksi sesi terlantar; usulkan batas4jam per occurrence untuk rilis awal, dengan peringatan sebelum batas. Angka ini rekomendasi, bukan kemampuan terbukti.
- Pilihan simpan membuat snapshot manifest yang berisi seluruh objek dan status kelengkapan. Commit idempoten; kegagalan sebagian menghasilkan status jelas, bukan sukses palsu. Keputusan simpan yang telah diterima tidak boleh kalah oleh expiry staging; transaksi/lock/generation mengatur race ini.
- Arsip disetujui tanpa expiry. Usulkan menyertakan transkrip asli juga agar terjemahan dapat diperiksa; kebutuhan eksplisit pengguna baru mencakup catatan terjemahan.
- Pilihan tidak menyimpan menjalankan barrier yang menolak hasil job terlambat, lalu membersihkan staging dan turunannya. Jangan menghapus dokumen meeting lain.
- Hapus manual tetap tersedia sebagai rekomendasi; “selamanya” berarti tidak kedaluwarsa otomatis, bukan tidak dapat dihapus pemilik.
- Backup/PITR/vendor log memiliki batas tersendiri; jangan mengklaim hapus langsung pada semua salinan eksternal. Penghapusan arsip harus dipertahankan saat restore agar data tidak muncul kembali.

## 7. Terjemahan dan kualitas sumber

Rekomendasi: tampilkan transkrip sumber yang tertaut ke terjemahan; AI memakai keduanya agar kesalahan terjemahan tidak tersembunyi. Partial boleh diperlihatkan sebagai sementara; pemicu pertanyaan memakai final dan konteks berurutan. Terjemahan final mempunyai ID sumber dan revision, sehingga update tidak menjadi baris duplikat.

“Bahasa dari mana saja” adalah tujuan multibahasa, bukan klaim provider mendukung semua bahasa. Wajib membuat matriks bahasa/model yang diuji, termasuk code-switching; bahasa tidak didukung harus dinyatakan. Provider/model terjemahan belum dipilih atau diuji. Usulan memakai adapter Sumopod yang ada bila hasil evaluasi memenuhi kualitas/latensi, bukan mengarang nama model.

Terjemahan gagal tidak menjadi transkrip kosong seolah pengguna diam. Pertahankan sumber dan status gagal. Gangguan audio ditandai sebagai gap; jangan mengklaim rekaman/transkrip lengkap jika ada frame hilang.

## 8. Dokumen dan bukti

Kebutuhan mencakup PDF, DOCX, spreadsheet. Rekomendasi konkret untuk draft: PDF teks maupun scan (OCR), DOCX, XLSX dan CSV; XLS/ODS dan konektor Google Sheets tidak otomatis termasuk. Pilihan ini perlu persetujuan scope, bukan klaim dukungan sudah tersedia.

Setiap format punya parser sendiri dengan output bersama: document_version, struktur bagian, locator, teks dan kualitas ekstraksi. PDF: halaman; DOCX: heading/paragraf/tabel; XLSX: sheet/rentang sel/header/satuan; CSV: baris/kolom. DOCX tidak memakai nomor halaman yang bergantung layout. Dokumen scan yang OCR-nya gagal/tidak jelas tidak boleh dianggap siap.

Rekomendasi batas awal:10file/meeting,20MiB/file,100MiB total; batas jumlah halaman/sel/dekompresi dan timeout parser wajib diperinci pada plan. File terenkripsi/password-protected ditolak jelas. Tidak menjalankan macro, formula, embedded object, atau fetch link eksternal. Nilai formula hanya boleh dipakai bila nilai tersimpan dan statusnya dijelaskan; tidak menjanjikan recalculation.

Retrieval harus memakai dokumen yang sudah siap pada occurrence terkait. File yang diunggah ulang membuat versi baru; citation lama tetap menunjuk versi lama, bukan berubah diam-diam. Konten dokumen tidak boleh mengubah system instruction atau izin agent. Delete harus membatalkan job dan membersihkan object/chunk/index/cache terkait.

Pemilihan retrieval dimulai dari evaluasi korpus multilingual representatif: lexical/hybrid atau embedding dipilih berdasarkan bukti recall dan biaya. Tidak menetapkan vector database baru sebelum evaluasi. Penyimpanan file private membutuhkan ownership policy sendiri selain RLS tabel.

## 9. Pertanyaan dan jawaban bilingual

Semua pertanyaan peserta lain diproses tanpa identifikasi penerima. Question memiliki ID stabil, source segment IDs, urutan tampilan, versi dan status. Pisahkan beberapa pertanyaan berbeda dalam satu ucapan; jangan membuat duplikat karena partial/final atau hasil yang datang ulang. Jangan menghapus pertanyaan lanjutan hanya karena kemiripan kata.

Pipeline rekomendasi: deteksi pertanyaan dari final → retrieval terbatas owner+occurrence → draft berbukti → cek citation dan kesetaraan Indonesia/Inggris → maksimal satu revisi bila diperlukan. Tidak memakai agent loop tanpa batas. Jumlah putaran/timeout menjadi konfigurasi backend, bukan parameter bebas client.

- Pertanyaan tetap ada walau tidak ada jawaban.
- Tanpa dokumen/bukti: jawaban Indonesia dan Inggris `-`, tanpa citation palsu.
- Parsing/retrieval/provider failure berbeda dari tidak ada bukti; tampilkan status gagal, bukan `-` sebagai keputusan faktual.
- Bukti harus mendukung klaim, bukan hanya kutipan yang kebetulan terdapat dalam file. Pertahankan angka, satuan, negasi dan batas kepastian.
- Kutipan verbatim mempertahankan bahasa sumber. Jika diterjemahkan, tandai terjemahan kutipan.
- Revisi memperbarui item/version yang sama, tidak mengganti rujukan item lain.
- Pertanyaan hanya dipetakan dari audio peserta lain; sistem tidak mengetahui apakah pengguna sudah menjawab melalui mikrofon. Tidak mengklaim status “sudah dijawab pengguna”.

## 10. Keamanan dan operasi tim kecil

Keputusan pengguna: owner-only, tanpa sharing antarrekan. Ini berlaku walaupun mereka mengikuti meeting yang sama. Tidak ada team-wide search, public link atau admin aplikasi yang otomatis membaca isi pengguna lain.

Rekomendasi: akun disediakan untuk anggota yang diizinkan, tanpa signup publik. Aplikasi memakai autentikasi dan RLS yang ada; file privat serta signed URL bila dipakai harus singkat, tidak menjadi link berbagi permanen. UUID atau cursor bukan otorisasi.

Kontrol wajib: JWT tervalidasi, ownership di setiap endpoint/job, RLS pada seluruh tabel baru, no-store untuk konten sensitif, TLS untuk deployment bersama, secret hanya backend, log tanpa isi dokumen/audio/transkrip/token. Metrics hanya metadata nonkonten. Data di provider STT/LLM/OCR harus dijelaskan; owner-only aplikasi bukan janji bahwa provider atau operator infrastruktur tidak pernah memproses data.

Dokumen tidak tepercaya: batasi ukuran/dekompresi/waktu/memori, jalankan parser dengan privilege minimum, larang jaringan parser bila tidak diperlukan. Jangan menjadikan sanitasi prompt sebagai satu-satunya keamanan; otorisasi retrieval dan akses file tetap deterministik.

Backup, restore teruji, pemantauan kapasitas dan rotasi credential tetap diperlukan meski pengguna sedikit. Tidak ada automatic TTL arsip saat kuota hampir penuh: hentikan upload baru secara jelas dan beri operator peringatan. Storage tak berbatas waktu tidak berarti kapasitas tak terbatas.

## 11. Performa dan failure isolation

Angka berikut **target usulan untuk dievaluasi**, bukan hasil benchmark atau SLA provider:

| Ukuran | Target awal |
| --- | --- |
| Uji bersamaan |3pengguna, masing-masing1meeting aktif |
| Terjemahan final |p95 ≤3detik setelah final STT diterima backend |
| Jawaban bilingual berbukti |p95 ≤8detik sejak pertanyaan final, dokumen sudah siap |
| Deadline satu pekerjaan jawaban |15detik termasuk pemeriksaan/revisi |
| Antrean |Bounded per meeting dan global; kapasitas rinci dalam plan |

Pisahkan latency audio→STT dari STT→terjemahan dan pertanyaan→jawaban. Laporkan p50/p95 beserta bahasa, jaringan, model, ukuran dokumen, concurrency dan jumlah sampel. Jangan menghitung hanya request sukses.

Audio tidak menunggu parser/LLM/arsip. Batasi job AI per occurrence dan global; versi lama yang tidak diperlukan dibatalkan. Jangan menyalin seluruh dokumen untuk setiap pertanyaan atau mengulang indexing tanpa perubahan versi. Hindari alokasi/copy audio tambahan yang tidak diperlukan; benchmark sebelum mengubah transport.

Rate limit, timeout, quota habis, restart, SSE reconnect dan provider outage harus menghasilkan status nyata. Retry hanya operasi yang aman/idempoten dan dalam budget; tidak mengulang biaya tanpa batas atau menghasilkan final ganda. Tidak ada request provider berbayar otomatis saat startup/test biasa.

## 12. Cakupan rilis dan prioritas

| Prioritas | Paket | Alasan/dependensi |
| --- | --- | --- |
| P0-0 | Konsolidasi PRD/plan lama dan keputusan | Mencegah AI mengeksekusi arsitektur lama |
| P0-1 | Identitas occurrence, owner-only, consent pascameeting, arsip tanpa expiry | Dasar semua data; revisi schema/purge paling berisiko |
| P0-2 | Validasi capture native + tawaran koneksi + lifecycle | Bukti Chrome belum lengkap; jangan membangun janji otomatis di atas asumsi |
| P0-3 | Terjemahan Indonesia dan catatan sumber tertaut | Nilai utama; matriks bahasa dan latency |
| P0-4 | Upload/ekstraksi/retrieval/citation | Prasyarat jawaban dokumen |
| P0-5 | Pertanyaan terstruktur + jawaban bilingual bounded | Bergantung source stable dan dokumen siap |
| P0-6 | Arsip lengkap, history, hapus/ekspor dan pemulihan | Mengubah pilihan simpan menjadi perilaku end-to-end |
| P0-7 | Security/failure/latency gates dan distribusi privat | Rilis hanya setelah semua alur utama tervalidasi |

Keamanan, privasi dan bounded resources dikerjakan pada setiap paket, bukan baru ditambahkan di P0-7. P0-7 adalah verifikasi akhir. Detil desain dapat berjalan paralel setelah kontrak bersama disepakati; eksekusi mengikuti dependency.

Fitur roadmap lama tidak dihapus diam-diam. Profile edit, pergantian akun, reset password, onboarding, glosarium, diarization, self-hosted/provider tambahan, presence audio, ringkasan/action items, tema/lokalisasi, provider redundancy, observability lanjutan dan Store listing tetap masuk matriks cakupan paket plan. Rekomendasi: tunda fitur yang bukan alur inti; reset/recovery dan operasional minimum tetap dibutuhkan untuk tim. Jangan menganggap daftar tertunda sebagai persetujuan membatalkan fitur. Tidak ada mic capture, jawaban dikirim otomatis, sharing tim atau RAG lintas meeting.

## 13. Kontrak paket planning untuk AI pelaksana

Setelah desain disetujui, buat **satu dokumen utama plan** yang menunjuk detail, bukan beberapa plan bersaing. Roadmap tetap daftar status/bukti, tidak menggandakan langkah implementasi.

Detail yang wajib disusun: PRD/traceability seluruh ID fitur; arsitektur/modul; lifecycle/API/event/error/schema; migration/rollback; streaming/translation; ingestion/retrieval/citation; assistance bilingual; arsip/privacy; pengalaman extension; operasi/performa; verifikasi/quality gates.

Setiap paket task wajib memuat: requirement ID, prasyarat, lokasi kode yang benar-benar ditelusuri, kontrak input/output, state transition, invariants, langkah perubahan, concurrency/idempotency, error/cancel, security, migrasi dan compatibility cutover, smoke end-to-end, expected result, kriteria selesai, file dokumentasi yang diperbarui, serta larangan scope. Tidak menyebut file usulan sebagai file existing. Jangan menaruh placeholder yang kemudian dianggap implementasi selesai.

Mapping fitur lama memuat retain/revise/defer/superseded beserta alasan dan plan pengganti. Lindungi baseline kerja pengguna; jangan mengubah history test menjadi hasil verifikasi baru. Plan dokumen tidak memberi izin implementasi pada sesi ini.

## 14. Review desain dan batas bukti

Pemeriksaan dokumen oleh penulis: requirement pengguna dipisahkan dari rekomendasi, arsip permanen dipisahkan dari staging24jam usulan, owner-only mencakup file/retrieval, dokumen tidak dibagi lintas occurrence, tanpa bukti tidak memakai pengetahuan umum, bahasa jawaban berbeda dari target terjemahan, dan prioritas revisi consent/expiry ditempatkan sebelum fitur baru.

Belum ada review independen, uji runtime, load test, verifikasi provider/model/OCR atau penilaian kualitas bilingual pada sesi ini. PRD/desain sudah disetujui pengguna; paket plan eksekusi detail seluruh fitur belum disusun. Pemeriksaan struktur dokumen menemukan15ID requirement unik dan2tautan lokal valid; bukan verifikasi aplikasi.

Pilihan utama yang disetujui sebagai desain: satu backend modular; staging privat dengan batas24jam setelah meeting; batas awal4jam/meeting; arsip menyertakan transkrip asli; PDF scan/OCR + DOCX/XLSX/CSV; batas upload yang disebut; pipeline maksimal satu revisi; dan target performa sebagai sasaran evaluasi, bukan hasil terbukti. Detail API/library belum ditetapkan tanpa pemeriksaan sumber dan dokumentasi. Dilarang melanjutkan implementasi hanya berdasarkan persetujuan ini.
