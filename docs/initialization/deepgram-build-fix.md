# Perbaikan build Deepgram — 2026-10-18

Proyek: LiveTranslate. Sumber kode: `backend/internal/services/deepgram.go`.

Build gagal karena `listen.NewREST` pada SDK v3.8.0 menghasilkan RESTClient tanpa method `FromFile`. Source dan contoh SDK terpasang menunjukkan wrapper `pkg/api/listen/v1/rest.New(client)` yang menyediakan method tersebut. Pemanggilan diperbaiki menjadi `api.New(client).FromFile(...)`, tanpa mengubah versi dependency.

Verifikasi: error kompilasi berhasil direproduksi sebelum perbaikan. Setelah perbaikan, `go test -buildvcs=false ./...`, `go vet -buildvcs=false ./...`, serta `CGO_ENABLED=0 go build -buildvcs=false -trimpath -o /tmp/livetranslate-server ./cmd/server` lulus. Flag buildvcs=false hanya dipakai untuk verifikasi lokal karena metadata Git workspace tidak dapat dibaca; Dockerfile tidak diubah.

Build Docker dicoba tetapi akses docker.sock ditolak sandbox. Request API Deepgram tidak dijalankan. Jalankan kembali `docker compose up --build -d backend` pada terminal pengguna.
