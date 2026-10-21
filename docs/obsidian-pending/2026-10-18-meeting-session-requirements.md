# Automatic Meeting Session Registration

Status: draft requirement untuk review; belum diimplementasikan.
Proyek: LiveTranslate.
Prioritas fitur: P0 — fondasi isolasi konteks dan history LLM per sesi meeting.
Cakupan: frontend Chrome extension, backend Go, dan migrasi database Supabase.

## Tujuan

Mendeteksi otomatis bahwa pengguna sudah bergabung dalam meeting browser yang didukung, lalu mendaftarkan sesi milik pengguna pada database. Setiap sesi memiliki meeting_id UUID backend yang menjadi pengenal untuk fitur audio, transkrip, dan LLM berikutnya. Fitur ini tidak menangkap audio dan tidak mengirim request LLM.

## Functional requirements

| ID | Prioritas | Requirement |
| --- | --- | --- |
| FR-01 | P0 | Setelah login dan izin extension tersedia, deteksi tab aktif pada jendela Chrome yang sedang difokuskan tanpa membuka popup atau menekan tombol Mulai. Periksa ulang saat aktivasi tab, perubahan URL, perubahan status halaman meeting, dan login selesai. |
| FR-02 | P0 | Dukung Google Meet, Zoom Web App, dan Jitsi meet.jit.si. Gunakan host/path yang didukung serta adapter status bergabung per provider. Homepage, undangan yang meluncurkan aplikasi desktop, login, pre-join, waiting room, dan domain tiruan tidak mendaftarkan sesi. Status yang belum dapat dipastikan tidak mendaftarkan sesi. |
| FR-03 | P0 | Setelah status bergabung terdeteksi pada tab aktif, backend menyimpan sesi berisi id, user_id, provider, provider_host, provider_room_id, status, started_at, dan ended_at. user_id berasal dari identitas pengguna yang diverifikasi backend. |
| FR-04 | P0 | Backend mengembalikan meeting_id UUID. Identitas ruang provider dan Chrome tab_id bukan identitas sesi; meeting pada ruang yang sama setelah sesi sebelumnya berakhir menghasilkan meeting_id baru. |
| FR-05 | P0 | Event berulang, retry request, dan reload tab pada sesi yang masih berlangsung menggunakan meeting_id yang sama. Extension menyimpan pemetaan tab-ke-sesi dan kunci idempotensi agar request yang diulang tidak membuat sesi ganda. Dua tab meeting berbeda memiliki sesi terpisah, termasuk jika menggunakan ruang yang sama. |
| FR-06 | P0 | Berpindah fokus ke tab lain tidak mengakhiri sesi yang telah terdaftar. Akhiri sesi ketika adapter mendeteksi keluar meeting, tab ditutup, atau navigasi meninggalkan meeting. Reload yang kembali ke meeting yang sama tidak dianggap sesi baru. Status penyelesaian harus dapat disinkronkan ulang ketika koneksi pulih. |
| FR-07 | P0 | Endpoint sesi memerlukan autentikasi dan pemeriksaan kepemilikan; database menerapkan RLS. Setiap akses sesi dibatasi ke pengguna pemilik. Fitur downstream wajib memakai meeting_id dan kepemilikan yang sama untuk konteks serta history LLM. |
| FR-08 | P0 | Gangguan jaringan dan restart service worker tidak membuat UI melaporkan sesi tersimpan sebelum backend mengonfirmasi. Simpan operasi pending dan lanjutkan sinkronisasi dengan kunci yang sama saat koneksi tersedia. Logout menghapus akses dan pemetaan sesi akun dari extension agar tidak digunakan akun berikutnya. |
| FR-09 | P1 | Popup menampilkan status deteksi/sinkronisasi untuk tab saat ini: bukan meeting, belum bergabung, menyinkronkan, terdaftar, atau gagal sinkronisasi. Kesalahan tidak menampilkan token atau URL sensitif. |

## Dampak teknis

- Frontend diperlukan: service worker Manifest V3, content script/adapter provider, izin host terbatas, pemantauan lifecycle tab, penyimpanan state pending, dan indikator status popup.
- activeTab saja tidak cukup untuk deteksi otomatis karena izinnya bergantung pada tindakan pengguna. Jangan meminta tabCapture, offscreen, mikrofon, atau akses semua situs untuk fitur ini.
- Backend mengikuti struktur internal/meeting/{handler.go,service.go,repository.go,model.go}. Endpoint minimum yang diusulkan: POST /api/meetings untuk pendaftaran idempoten, GET /api/meetings untuk daftar sesi pengguna, dan POST /api/meetings/{id}/end untuk akhir sesi idempoten.
- Tambahkan tabel meetings, FK pengguna, constraint idempotensi per pengguna, indeks akses kepemilikan, dan RLS melalui migrasi baru. Jangan menyimpan token/passcode dari query/fragment URL.
- Belum membuat tabel pesan LLM, pipeline AI, streaming, transkrip, atau rekaman audio. meeting_id disediakan sebagai kontrak integrasi fitur berikutnya.

## Dependencies dan urutan prioritas

| Dependency | Status | Hubungan |
| --- | --- | --- |
| Autentikasi dan identitas pengguna | Sudah ada | Dependency wajib untuk kepemilikan sesi; alur token/refresh harus dapat dipakai service worker. |
| Supabase Auth/Data API dan profil pengguna | Fondasi sudah ada | Infrastruktur database; perlu migrasi tabel meetings dan RLS baru. |
| Background extension dan adapter meeting | Belum ada | Bagian pekerjaan fitur ini, menjadi penghasil event pendaftaran sesi. |
| Meeting Session Registration | Fitur ini, P0 | Harus tersedia sebelum fitur audio/transkrip/LLM menyimpan data per meeting. |
| Indikator status popup | P1 | Dikerjakan sesudah alur deteksi, registrasi, dan isolasi sesi berfungsi. |
| Audio Capture / Transcription / LLM History | Fitur berikutnya | Bergantung pada meeting_id; bukan dependency untuk mengerjakan fitur ini. |

Urutan implementasi: database dan API sesi → deteksi/provider adapter serta sinkronisasi extension → indikator popup → verifikasi integrasi seluruh provider.

## Kriteria selesai

1. Meeting aktif yang sudah diikuti pada ketiga provider terdaftar otomatis dengan akun login; tidak perlu membuka popup. Pemeriksaan nyata dilakukan pada Chrome dengan provider yang didukung.
2. Tab biasa, domain tiruan, homepage provider, pre-join, waiting room, dan peluncur aplikasi desktop tidak menghasilkan baris meeting.
3. Aktivasi tab berulang, reload, event ganda, retry request, dan restart service worker tidak menggandakan sesi yang sama.
4. Dua tab meeting menghasilkan meeting_id berbeda. Pertemuan baru pada ruang yang sebelumnya sudah diakhiri juga menghasilkan meeting_id berbeda.
5. Perpindahan fokus mempertahankan sesi. Keluar meeting, penutupan tab, dan navigasi keluar mengakhiri sesi; retry pengakhiran aman.
6. Pengguna A tidak dapat membaca atau mengakhiri sesi pengguna B, baik melalui API Go maupun akses Data API yang diizinkan RLS.
7. Putus-sambung jaringan memulihkan sinkronisasi tanpa duplikasi. Operasi milik akun lama tidak diteruskan memakai identitas akun baru setelah logout/login.
8. Kontrak API mengembalikan meeting_id stabil yang siap dipakai fitur LLM; seluruh test backend, test extension, build extension, dan pemeriksaan integrasi fitur lulus. Isolasi history LLM aktual diverifikasi ketika fitur LLM diimplementasikan.
9. Tidak ada perekaman audio, request Deepgram/Sumopod, atau penyimpanan passcode/token URL sebagai bagian fitur ini.

## Asumsi/default yang perlu dikonfirmasi dalam desain implementasi

- Satu sesi adalah satu kehadiran pengguna pada satu tab meeting; bukan satu objek meeting global yang dibagikan seluruh peserta.
- Jitsi tahap awal hanya meet.jit.si; domain self-hosted dan embedded meeting belum termasuk.
- Deteksi dimulai dari tab aktif pada jendela Chrome yang difokuskan. Sesi yang telah terdaftar tetap dipantau saat tab berada di background.
- Penutupan browser yang tidak normal tidak selalu mengirim event akhir. Rekonsiliasi sesi yang tertinggal perlu ditetapkan dalam desain lifecycle; backend tidak boleh mengklaim mengetahui waktu berakhir aktual tanpa bukti.

## Referensi

- https://developer.chrome.com/docs/extensions/reference/api/tabs
- https://developer.chrome.com/docs/extensions/develop/concepts/activeTab
- https://support.zoom.com/hc/en/article?id=zm_kb&sysparm_article=KB0064261
- https://jitsi.github.io/handbook/
