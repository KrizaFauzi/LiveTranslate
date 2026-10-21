# Audio pemeriksaan

Letakkan audio pendek milik pengguna yang tidak sensitif di `fixtures/check.wav`.
File audio diabaikan Git dan dimount read-only sebagai `/app/fixtures/check.wav`.
Pemeriksaan Deepgram memerlukan API key, model akun, dan dapat memakai kuota.
Tidak ada perekaman mikrofon otomatis. Prerecorded check tidak memverifikasi streaming.

Fixture pengganti yang aman dipakai pada verifikasi 2026-10-22 (bukan data pengguna):
sampel SDK Deepgram berisi beberapa detik suara manusia.

```bash
cp "$(go env GOMODCACHE)/github.com/deepgram/deepgram-go-sdk/v3@v3.8.0/examples/speech-to-text/websocket/replay/testing.wav" fixtures/check.wav
docker compose run --rm backend -check deepgram -audio /app/fixtures/check.wav
```

Untuk mengunggah chunk PCM16 mono 8 kHz (format `linear16` yang dipakai backend):

```bash
ffmpeg -y -i fixtures/check.wav -f s16le -acodec pcm_s16le -ar 8000 -ac 1 /tmp/pcm16_8k.raw
```
