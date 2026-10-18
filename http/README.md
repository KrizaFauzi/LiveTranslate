# HTTP dengan Resterm

Isi email dan password biasa di http/resterm.env.json. Nama hanya dipakai untuk registrasi dan default-nya Pengguna. File lokal ini diabaikan Git.

Jika file belum ada:

```bash
cp http/resterm.env.example.json http/resterm.env.json
```

Buka Resterm:

```bash
resterm -e local -E http/resterm.env.json http/auth.http
```

Pilih request yang dibutuhkan: Health, Register, Login, GetProfile, Refresh, atau Logout. Setelah Register, konfirmasi email jika Supabase memintanya. Jalankan Login sebelum GetProfile/Refresh/Logout. Token diteruskan otomatis dalam memori.

Untuk menjalankan login sampai logout sekaligus:

```bash
resterm run -e local -E http/resterm.env.json -W auth-session -f text http/auth.http
```

Ubah baseUrl jika alamat backend berbeda. Status 404 berarti route tidak tersedia pada backend yang diakses; rebuild backend. Status 503 berarti konfigurasi/service auth belum siap. Redirect lokal yang didukung adalah http://localhost:8989/auth/confirmed dan harus ditambahkan pada Redirect URLs Supabase.

Pengujian HTTP live dari sandbox agent masih dibatasi izin socket. Hasil terperinci ada di docs/implementation/auth-status.md.
