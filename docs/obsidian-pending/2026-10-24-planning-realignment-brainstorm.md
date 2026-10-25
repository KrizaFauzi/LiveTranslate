# LiveTranslate — brainstorming penyelarasan planning

Tanggal: 2026-10-24. Status: pengumpulan konteks; belum ada persetujuan desain baru atau implementation plan baru.

## Permintaan pengguna

Hanya brainstorming dan planning lengkap seluruh fitur, tanpa implementasi. Selaraskan semua planning dengan PRD; bila PRD belum tersedia, pahami konteks dan diskusikan terlebih dahulu. Gunakan satu dokumen utama dengan dokumen detail; nyatakan prioritas sekarang dan prioritaskan revisi desain bila diperlukan. Rencana harus dapat dieksekusi model AI lain serta mencakup modularitas, kecepatan eksekusi, efisiensi layanan/microservice, dan keamanan data.

## Temuan dokumen

- Pencarian isi dan inventaris nama dokumen repository tidak menemukan PRD produk lengkap. Plan inisialisasi menyebut `PRD LiveTranslate` dan `Tech Stack & Architecture`, tetapi tidak memuat dokumen sumber itu. Ini bukan bukti PRD tidak ada di luar repository.
- Obsidian `Proyek/LiveTranslate` gagal diakses lewat Local REST API, termasuk satu retry pada sesi ini. Isi PRD/vault belum dapat diverifikasi; tidak ada write/read-back vault.
- Roadmap saat ini menjadi baseline lokal: extension React/JavaScript MV3, satu backend Go, Supabase Auth/Data API/RLS, Deepgram, Sumopod. Larangan dependency/service tambahan tetap berlaku sampai ada desain baru disetujui.
- Konflik instruksi lama: plan inisialisasi masih menyebut pgx/database langsung; spec/plan auth 2026-10-18 masih menyebut Node/Drizzle. README dan status auth menyatakan arsitektur terkini Go langsung ke Supabase HTTP API. Dokumen lama perlu label historis/pengganti agar tidak dieksekusi ulang oleh AI.
- Requirement meeting awal memakai joined-state dan identitas occurrence sebagai meeting_id, sedangkan README/roadmap terkini memakai identitas link stabil dan meeting_run_id per capture. Baseline terbaru tidak boleh dibatalkan tanpa keputusan eksplisit.
- Berdasarkan bukti historis dalam roadmap, fondasi/auth/meeting/capture/transkripsi/storage tersedia dengan batas validasi target/cloud/native Chrome. Bukti itu tidak dijalankan ulang pada sesi perencanaan ini.
- Translation, AI assistance, RAG dan ringkasan masih memiliki keputusan produk terbuka. Tidak boleh mengisi keputusan tersebut seolah sudah disetujui.

## Usulan untuk dibahas, bukan keputusan

Prioritaskan penyelarasan PRD dan hierarki sumber dokumen, lalu batas modul/kontrak data dan layanan serta keamanan/performa, sebelum detail implementasi fitur. Modularitas tidak otomatis membutuhkan banyak service; pemisahan deployment harus punya alasan kapasitas, isolasi kegagalan, atau operasi yang terukur.

## Kebutuhan yang dinyatakan pengguna setelah penelusuran

1. Ketika pengguna masuk meeting, extension aktif otomatis dan menanyakan apakah ingin terhubung dengan LiveTranslate.
2. Audio peserta lain dari tab meeting diterjemahkan otomatis ke bahasa Indonesia; bahasa sumber dapat beragam. Pengguna menegaskan mikrofon sendiri tidak perlu ditranskripsikan atau diterjemahkan.
3. Catatan terjemahan terlihat selama meeting. Sesudah meeting selesai, pengguna ditanya apakah catatan perlu disimpan atau tidak. Ini mengganti waktu consent penyimpanan F6 lama (sebelum capture), bukan persetujuan menyimpan permanen otomatis.
4. Catatan menjadi konteks LLM untuk memetakan pertanyaan yang perlu dijawab pengguna.
5. Bila pengguna sudah mengunggah dokumen khusus, LLM membuat jawaban otomatis berdasarkan dokumen tersebut, dengan kutipan sumber yang dapat diperiksa.
6. Pengguna membuka kemungkinan agent yang memoles jawaban melalui beberapa iterasi; arsitektur agent dan jumlah iterasi belum diputuskan.
7. Keluarga format dokumen yang disebut pengguna: PDF, DOCX, dan spreadsheets. Varian file spreadsheet serta isi dokumen belum ditentukan; jangan menganggap ini sudah mencakup XLS/XLSX/CSV/ODS, Google Sheets API, atau OCR.
8. Dokumen diunggah khusus per meeting, bukan perpustakaan dokumen akun yang dipakai ulang lintas meeting.
9. Pengguna menyetujui dokumen sumber dan jawaban AI ikut disimpan ketika memilih menyimpan hasil meeting. Paket arsip mencakup catatan terjemahan, pertanyaan/jawaban AI, dokumen sumber dan kutipannya; cakupan harus dijelaskan sebelum persetujuan.
10. Pengguna menolak batas30hari: paket arsip yang dipilih untuk disimpan disimpan selamanya, yaitu tanpa kedaluwarsa/penghapusan otomatis berbasis umur. Ini bukan permintaan menghapus data sekarang dan tidak memberi izin mengarsipkan sesi yang belum disetujui.

### Konsekuensi perencanaan

- Retrieval dokumen dan citation menjadi bagian alur nilai utama, bukan lagi sekadar optimasi opsional saat sliding context tidak cukup. Mekanisme embedding/vector database belum ditetapkan.
- Kontrak deteksi meeting → tawaran koneksi → persetujuan capture perlu direvisi. Interpretasi aman penulis: otomatis mendeteksi/menawarkan, tetapi audio tidak dikirim sebelum pengguna menyetujui. Ini belum mengubah implementasi.
- Bahasa tujuan Indonesia sudah ditetapkan. Cakupan bahasa sumber harus dipetakan ke kemampuan provider yang diverifikasi; jangan menjanjikan semua bahasa tanpa batas.
- Cakupan audio sudah diklarifikasi: hanya peserta lain dari audio tab; tidak mengakses mikrofon pengguna. Keputusan tab-only sebelumnya dipertahankan. Batas teknis: audio tab adalah campuran keluaran meeting, bukan jaminan pemisahan identitas pembicara; pantulan suara pengguna dari peserta lain tidak otomatis dapat disaring.
- Keputusan terbaru: keputusan simpan diberikan sesudah meeting, sementara catatan dapat dibaca selama meeting. Revisi consent/lifecycle F6 menjadi P0 perancangan. Catatan sesi sementara harus dibedakan dari arsip yang disetujui; jangan menyimpan permanen sejak awal lalu hanya menawarkan hapus di akhir.
- Desain revisi wajib mencakup kelengkapan seluruh catatan (bukan hanya cache200final), batas memori/durasi, akhir meeting versus stop capture, kesempatan memilih setelah tab ditutup, browser/backend crash, timeout keputusan, save idempoten/atomik, dan discard yang membersihkan konteks sementara. Pemrosesan sementara di server/provider tetap perlu diungkapkan.
- Retensi30hari F6 lama digantikan target penyimpanan arsip tanpa batas waktu otomatis. Revisi expiry/RLS/purge menjadi bagian P0; arsip dan staging harus dipisahkan agar pembersihan data sementara tidak menghapus arsip. Jangan sekadar mematikan seluruh cleanup. Transkrip asli sebagai bagian arsip, lokasi staging serta kebijakan saat pengguna tidak menjawab masih perlu desain. Tidak ada izin menyimpan audio mentah.
- Usulan penulis: konteks menyertakan transkrip asli dan terjemahan yang saling tertaut, sehingga kesalahan terjemahan tidak disembunyikan dari pipeline AI.
- Usulan penulis: pipeline retrieve → draft → periksa dukungan kutipan → revisi terbatas; bukan agent loop tanpa batas. Citation bukan bukti otomatis bahwa klaim benar; bukti tidak cukup harus dinyatakan, bukan dilengkapi dengan fakta karangan.
- Belum ada izin mengirim jawaban ke peserta atau menyuarakan jawaban otomatis; menghasilkan saran untuk pengguna berbeda dari bertindak atas namanya.
- Keluarga format sudah disebut: PDF, DOCX, spreadsheets; upload per meeting dan retensi arsip tanpa kedaluwarsa sudah ditetapkan. Varian spreadsheet, PDF hasil scan/OCR, batas ukuran, lifecycle data sementara, identifikasi pertanyaan untuk pengguna, serta perilaku tanpa dokumen masih memerlukan desain.
- Usulan penulis untuk ingestion modular: parser terpisah per format, menghasilkan struktur sumber bersama dengan versi dokumen dan locator kutipan. PDF memakai halaman; DOCX memakai heading/paragraf/tabel, bukan nomor halaman yang bergantung layout; spreadsheet memakai sheet/rentang sel dan header/satuan terkait. Jangan meratakan seluruh tabel menjadi teks tanpa konteks.
- Usulan keamanan: validasi tipe/ukuran/dekompresi dan batas resource parser, tanpa menjalankan macro/formula atau mengambil tautan eksternal. Isi dokumen diperlakukan sebagai data tidak tepercaya, bukan instruksi agent. Rumus/cached values dan OCR memerlukan kebijakan kualitas eksplisit; kegagalan ekstraksi tidak boleh diklaim sebagai dokumen siap.
- Keputusan pengguna menggantikan usulan perpustakaan akun: dokumen hanya untuk meeting tempat diunggah. Isolasi harus mencakup file, hasil ekstraksi, indeks, retrieval, cache dan kutipan; kepemilikan akun saja tidak cukup. Meeting lain tidak boleh mengambil dokumen tersebut otomatis, termasuk saat menggunakan URL ruang yang sama di hari berbeda. Pemetaan occurrence produk ke meeting_id/run dan upload sebelum capture harus dirumuskan dalam desain.
- Paket arsip disetujui tanpa kedaluwarsa otomatis: catatan terjemahan, pertanyaan/jawaban AI, dokumen sumber dan kutipan. Usulan penulis: pertahankan hak hapus manual/ekspor dan bersihkan data sementara beserta turunannya bila tidak menyimpan. Mempertahankan sumber dalam arsip tidak mengizinkan retrieval lintas meeting. Desain operasi wajib memperhitungkan pertumbuhan kapasitas, backup/restore dan biaya tanpa menambah TTL tersembunyi atau menjanjikan storage tanpa batas kapasitas.

Prioritas usulan: selesaikan batas capture/consent dan kebutuhan produk; susun kontrak transkrip-terjemahan; naikkan upload/index/retrieval/citation ke jalur inti bantuan AI; baru tentukan iterasi agent berdasarkan latensi, biaya dan kualitas.

## Batas sesi dan kelanjutan

Tidak mengubah kode atau menaikkan status implementasi. Roadmap diperbarui untuk membedakan kebutuhan produk baru dari baseline yang sudah diimplementasikan. Inventaris planning lokal mencakup inisialisasi, auth, transkripsi, storage, spec terkait, requirement/brainstorm meeting, serta katalog roadmap. Pemeriksaan konsistensi lengkap dan penulisan plan menunggu penyelesaian pembahasan arah produk/PRD; jangan menganggap catatan ini plan siap eksekusi.

Tujuan sinkronisasi: `Obsidian Vault/Proyek/LiveTranslate/Notes/2026-10-24-planning-realignment-brainstorm.md`.

## Klarifikasi bantuan berbasis dokumen

Pengguna menetapkan: bila dokumen belum diunggah atau jawaban tidak didukung dokumen, jangan memberikan jawaban pengetahuan umum atau mengarang fakta/kutipan. Klarifikasi tampilan terbaru: pertanyaan tetap tercantum, jawaban kosong atau `-`; pilihan penulis memakai `-` secara konsisten. Status internal tetap membedakan tidak ada bukti dari belum diproses/gagal. Terjemahan dan catatan live tetap berjalan.

Kontrak teknis usulan: bedakan no-document/insufficient-evidence dari parsing/retrieval/provider failure; kegagalan sistem tidak membuktikan isi dokumen tidak memuat jawaban. Iterasi agent tidak boleh mengubah ketiadaan bukti menjadi kepastian palsu. Kebijakan ini menyelesaikan pertanyaan perilaku tanpa dokumen pada catatan di atas.

## Pemetaan pertanyaan — keputusan pengguna

- Proses pertanyaan yang terdengar dari peserta lain, meskipun ditujukan kepada peserta selain pengguna. Tidak memfilter berdasarkan nama, nama panggilan, atau tebakan penerima.
- Pengguna menghendaki daftar bernomor: setiap item memuat pertanyaan, jawabannya jika didukung dokumen, dan bagian dokumen sumber.
- Bila tidak ada jawaban dari dokumen, pertanyaan tetap ada dengan jawaban kosong atau `-`. Jangan menghasilkan jawaban generik.
- Usulan teknis penulis: question_id stabil dan rujukan segmen sumber; pertanyaan berbeda dalam satu ucapan dipisah, pengulangan akibat partial/final tidak membuat item ganda, revisi jawaban tetap pada item yang sama. Pertanyaan lanjutan yang mengubah makna tidak dihapus sebagai duplikat.
- Nomor adalah urutan tampilan, bukan primary key. Pemetaan jawaban/citation tidak boleh bergeser saat hasil AI selesai tidak berurutan.
- Ini menutup isu identifikasi sasaran pada catatan sebelumnya. Batas iterasi masih perlu dirancang; tidak ada implementasi.

## Bahasa jawaban — keputusan pengguna

Setiap jawaban AI tersedia dalam bahasa Indonesia dan bahasa Inggris yang baku serta sesuai tata bahasa. Ini bukan perubahan target terjemahan percakapan yang tetap bahasa Indonesia.

Kontrak kualitas usulan penulis: kedua versi merepresentasikan jawaban berbukti yang sama, mempertahankan angka/satuan/negasi/istilah dan batas kepastian, serta mengacu ke sumber yang sama. Pemolesan grammar tidak boleh menambah klaim. Kutipan verbatim mempertahankan teks asli sumber; terjemahan kutipan diberi label. Tanpa bukti, kedua versi jawaban `-`. Kualitas diuji pada sampel bilingual nyata, bukan hanya mengandalkan instruksi prompt.

## Skala penggunaan — keputusan pengguna

Target hanya tim terbatas, bahkan mungkin di bawah tiga orang. Bukan layanan publik dengan pendaftaran massal. Untuk rancangan kapasitas awal, penulis mengusulkan pengujian sampai tiga pengguna/meeting bersamaan sebagai margin, bukan requirement concurrency yang telah dinyatakan pengguna.

Implikasi arsitektur usulan penulis:

- Pertahankan satu deployment backend Go yang modular (auth, meeting, audio/transkripsi, terjemahan, dokumen/retrieval, assistance, arsip); bukan microservice per fitur. Parser dokumen yang memerlukan isolasi dapat memakai subprocess/resource boundary tanpa membuat jaringan service baru.
- Supabase tetap layanan data/identitas; Deepgram/Sumopod tetap adapter provider. Tidak menambah Kubernetes, Redis, broker eksternal atau framework multi-agent hanya karena memakai agent.
- Pipeline AI bounded: cari bukti, susun jawaban bilingual, verifikasi bukti/bahasa, revisi terbatas; tidak memblokir jalur audio. Detail batas numerik akan menjadi usulan terukur dalam desain.
- Akses aplikasi terbatas pada anggota yang diizinkan; kebijakan provisioning akan dirancang tanpa menganggap signup publik sebagai kebutuhan rilis.
- Pengguna menegaskan setiap anggota hanya boleh melihat milik sendiri. Owner-only berlaku pada meeting, file dokumen, hasil ekstraksi/indeks, retrieval, jawaban, kutipan, arsip dan ekspor; tanpa sharing tim.
- Penyimpanan permanen tetap membutuhkan backup/restore, batas upload/kapasitas yang terlihat, dan biaya operasional; tidak boleh diam-diam menggantinya dengan TTL.

## Draft konsolidasi untuk persetujuan

`docs/superpowers/specs/2026-10-24-product-realignment-design.md` menyatukan kebutuhan menjadi draft PRD/desain, bukan plan eksekusi. Rekomendasi yang belum disetujui dipisahkan: satu backend modular, staging privat24jam sesudah meeting, batas4jam/meeting, menyertakan transkrip asli dalam arsip, PDF scan/OCR dan DOCX/XLSX/CSV, batas upload, maksimal satu revisi AI dan target latency. Desain lengkap perlu persetujuan sebelum penulisan paket implementation plan. Tidak ada perubahan kode, migrasi atau verifikasi runtime.
