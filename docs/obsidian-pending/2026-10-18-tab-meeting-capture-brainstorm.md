# Tab Meeting Capture — rancangan untuk review

Status: desain disetujui pengguna; implementasi lokal selesai. Pengguna meminta langsung implementasi tanpa plan implementasi. Hasil dan batas verifikasi: docs/implementation/meeting-status.md.
Proyek: LiveTranslate. Tujuan vault: Obsidian Vault / Proyek/LiveTranslate/Notes/2026-10-18-tab-meeting-capture-brainstorm.md.

## Scope terbaru

Deteksi tab aktif pada jendela Chrome yang difokuskan sebagai halaman ruang Google Meet, Zoom browser, atau Jitsi. Tidak membuktikan pengguna sudah bergabung. Tidak menangkap audio, membaca DOM, atau memanggil LLM. Batas terbaru ini menggantikan usulan joined-state pada catatan meeting-session-requirements; catatan lama dipertahankan sebagai riwayat, bukan requirement final.

## Alternatif

1. Deteksi URL melalui service worker: rekomendasi untuk scope sekarang; sederhana dan tidak bergantung DOM provider.
2. Content script untuk memastikan joined-state: lebih presisi terhadap kehadiran, tetapi di luar scope sekarang.
3. Pemeriksaan saat popup dibuka: sederhana tetapi tidak otomatis ketika popup tertutup.

## Frontend

React popup menampilkan status; service worker MV3 menjalankan deteksi, autentikasi, registrasi, dan sinkronisasi tanpa popup terbuka. Listener dipasang sinkron pada top level. Pantau tabs.onActivated, tabs.onUpdated, windows.onFocusChanged, tabs.onRemoved, startup, dan login. Jangan membuat sesi baru untuk tab background. Tab yang sudah terdaftar tetap dipantau lifecycle-nya saat tidak aktif.

Gunakan host_permissions terbatas untuk Google Meet, Zoom browser termasuk subdomain regional yang disetujui, meet.jit.si, dan backend. activeTab saja tidak memenuhi deteksi otomatis. Tidak perlu akses semua situs, tabCapture, mikrofon, atau offscreen. URL bisa dibaca dengan host permission yang cocok tanpa permission tabs global.

Classifier menggunakan URL parser, HTTPS, pemeriksaan hostname dengan batas domain, dan adapter path provider. Tolak homepage, dashboard, link launcher native, path tidak dikenal, serta hostname tiruan. Meeting prejoin yang menggunakan URL ruang tetap dapat terdeteksi; tidak diklaim sudah bergabung. Daftar path Zoom harus divalidasi terhadap fixture URL web-client sebelum implementasi. MVP Jitsi hanya meet.jit.si; custom domain membutuhkan konfigurasi allowlist eksplisit. Simpan provider, host, room identity yang dinormalisasi; buang query/fragment dan passcode. Room Jitsi dinormalisasi lowercase mengikuti getNormalizedRoomName provider; aturan ini sudah diverifikasi dari source resmi.

Identitas sesi mengikuti link meeting per pengguna, bukan tab. Frontend menghasilkan meeting_key deterministik dari provider, host yang dikanonisasi menurut aturan provider, dan room ID. Simpan canonical_url yang mempertahankan identitas ruang tanpa passcode, tracking, atau parameter nonidentitas. Parameter yang menentukan ruang wajib diparse sebelum normalisasi. Pencocokan berdasarkan kesamaan meeting_key, bukan fuzzy matching atau kemiripan string. Alias path/host hanya disatukan jika aturan provider telah diverifikasi.

storage.local menyimpan cache user_id + meeting_key -> meeting_id dan canonical_url serta outbox tanpa token. storage.session hanya menyimpan pemetaan tab sementara. Refresh, tab kedua, tutup/buka kembali, dan browser restart mencari sesi link yang sama. Backend merupakan sumber kebenaran: cache miss tetap resolve ke sesi existing. Perubahan akun membersihkan cache aktif dan mencegah operasi akun lama dijalankan sebagai akun baru. Event diserialisasi per key dan respons async lama diperiksa terhadap akun serta revisi tab saat ini.

Menutup tab atau berpindah URL hanya melepas pemetaan tab; tidak menghapus atau mengganti sesi link dan history. Satu link yang sama digunakan kembali untuk akun yang sama. Konsekuensi: ruang berulang pada hari berbeda tetap satu sesi menurut requirement terbaru; pemisahan per pertemuan aktual kelak membutuhkan konsep run terpisah dan keputusan pengguna. Tidak ada auto-expiry atau pembuatan sesi baru berdasarkan tab ditutup.

## Backend

Ikuti Go internal/meeting/{handler,service,repository,model}. Integrasikan validasi token yang dapat dipakai ulang dengan auth yang sudah ada. Go mengakses Supabase Data API dengan publishable key dan JWT pengguna, mengikuti arsitektur saat ini; tidak menambah koneksi database langsung.

Tabel meeting_sessions: id UUID backend, user_id FK auth.users, meeting_key, canonical_url, provider, provider_host, provider_room_id, created_at. UNIQUE(user_id, meeting_key) dan indeks daftar milik pengguna. Identitas ruang yang dikanonisasi menjadi unique key sesi per akun sesuai requirement terbaru. URL lengkap dengan passcode tidak disimpan.

POST /api/meetings/resolve menerima metadata link tersanitasi. Backend menghitung ulang canonical_url dan meeting_key, tidak mempercayai key client. Insert atomik melalui unique constraint; conflict mengambil sesi existing dan mengembalikan meeting_id yang sama. Dua tab/perangkat/request paralel untuk link sama mendapat sesi yang sama. GET /api/meetings dan GET /api/meetings/{id} hanya membaca sesi pemilik. Endpoint end dan status open/closed dari draft sebelumnya tidak diperlukan untuk identitas sesi berbasis link ini.

Backend memvalidasi ulang kombinasi provider/host/room, tetapi tidak bisa membuktikan keadaan tab browser dari laporan client. user_id tidak diterima dari body. Aktifkan RLS owner-only untuk SELECT, INSERT, UPDATE beserta WITH CHECK dan grants minimum. Endpoint selalu mengecek autentikasi dan kepemilikan; akses melalui Data API langsung tetap dibatasi RLS. Auth gagal tidak menandai sesi tersimpan. Respons membedakan unsupported, pending, registered, dan sync-error.

Kontrak downstream: semua audio, transkrip, pesan, retrieval, dan request LLM harus terikat meeting_id dan pemilik. Context builder memuat history hanya dalam sesi yang diminta. Tidak membuat tabel pesan atau jalur LLM pada fitur ini; model/provider LLM yang sama tetap bisa melayani banyak sesi dengan konteks terpisah.

## Tahapan dan verifikasi

1. Sepakati semantik sesi dan fixture URL Google Meet/Zoom/Jitsi, termasuk scope domain Zoom dan Jitsi.
2. Migrasi tabel, grants/RLS, dan endpoint resolve Go idempoten.
3. Service worker, classifier, reuse autentikasi, state, dan outbox.
4. Indikator popup dan pengujian integrasi extension-backend.

Acceptance: URL didukung versus homepage/native launcher/domain tiruan; deteksi tanpa popup; tab fokus lintas window; event duplikat dan request paralel; reload ID tetap; dua tab link sama mendapat ID sama; ruang berbeda mendapat ID berbeda; query nonidentitas berbeda tetap ID sama; passcode tidak disimpan; cache hilang tetap resolve ID existing; tutup/buka kembali dan restart browser tetap ID sama; navigasi tidak menghapus history; worker restart; offline lalu retry; logout/login akun berbeda; isolasi dua pengguna melalui endpoint dan Data API/RLS. Browser crash tidak diklaim menghasilkan end event.

## Referensi

- https://developer.chrome.com/docs/extensions/reference/api/tabs
- https://developer.chrome.com/docs/extensions/reference/api/windows
- https://developer.chrome.com/docs/extensions/develop/concepts/activeTab
- https://developer.chrome.com/docs/extensions/develop/migrate/to-service-workers
- https://developer.chrome.com/docs/extensions/reference/api/storage

Dokumentasi Chrome diperiksa melalui Context7. Tool MCP Obsidian tidak tersedia pada sesi ini; belum disinkronkan ke vault.
