# Login/Register Implementation Plan

> **HISTORICAL — Superseded by 2026-10-24 execution planning.** This document describes the earlier Node/Drizzle auth architecture. See `2026-10-24-execution-master.md` and `2026-10-24-details/01-architecture-data.md` for the current design. Contents are preserved for history; do not execute.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Implementasikan register/login email-password dan sesi extension.
**Architecture:** FE React -> API Go -> service auth Node internal -> Supabase Auth dan Drizzle/PostgreSQL.
**Tech Stack:** React/Vite, Go net/http, Node/TypeScript, Supabase JS, Drizzle/postgres.js.
**Spec:** docs/superpowers/specs/2026-10-18-auth-design.md

## Global Constraints
- Ikuti skema profiles dan kontrak API dalam spec; migration/ di root.
- Tidak menyimpan rahasia dalam FE/migrasi; provider AI tidak dipanggil.
- Tidak mengubah repository lama. Root bukan Git repository; eksekusi di workspace ini tanpa commit/worktree.
- Pengguna memerintahkan "Jalankan" setelah review spec; eksekusi native dalam sesi ini.

## Review Focus
- Token pengguna A tidak boleh membaca profil B: test identitas terverifikasi dan RLS opt-in.
- Refresh token berotasi: test single-flight dan penyimpanan sebelum /me.
- Error jaringan tidak menghapus sesi: test FE.
- Header internal palsu tidak diteruskan: test proxy Go.
- Role runtime tidak bypass RLS dan metadata nama tidak valid: test database opt-in.

### Task 1: Auth service dan database
Files: auth-service/{package.json,tsconfig.json,drizzle.config.ts,Dockerfile}, auth-service/src/{server.ts,app.ts,provider.ts,db/schema.ts,db/client.ts}, auth-service/test/, migration/.
Consumes: environment dalam spec.
Produces: HTTP /api/auth/{register,login,refresh,logout,me}, /internal/database-check; makeHandler(deps), createProvider(config), createRepository(url).
- [x] Tulis test HTTP validasi, signup/login, refresh/logout, identitas terverifikasi dan kegagalan provider; jalankan RED.
- [x] Implementasikan handler dan adapter SDK/Drizzle; 8 test handler GREEN. Typecheck belum dapat dijalankan karena dependency tidak tersedia.
- [x] Tulis custom SQL/journal trigger/RLS/backfill/role; generator Drizzle Kit terblokir instalasi. Gunakan --custom untuk migrasi berikutnya.
- [x] Tambah test database opt-in pada database uji eksplisit.

### Task 2: Go gateway
Files: backend/internal/httpapi/{auth.go,auth_test.go,health.go}, backend/internal/config/config.go, backend/internal/services/{supabase.go,supabase_test.go}, backend/cmd/server/main.go.
Consumes: auth-service HTTP dan AUTH_SERVICE_URL/AUTH_INTERNAL_TOKEN.
Produces: public API /api/auth/*, check Supabase via Drizzle.
- [x] Tulis test proxy allowlist, no-store, konfigurasi kosong, body/rate limit, timeout dan secret palsu; jalankan RED.
- [x] Implementasi gateway dan pemeriksaan database; test/vet GREEN.

### Task 3: Extension
Files: extension/src/{auth.js,auth.test.js,App.jsx,style.css,config.js}, extension/public/manifest.json, extension/package.json.
Consumes: kontrak HTTP Task 2.
Produces: createAuthClient({fetchImpl,storage,baseUrl,now}), form Masuk/Daftar dan profil.
- [x] Tulis test persistensi sesi, refresh tunggal, rotasi token, 401, jaringan dan logout; jalankan RED.
- [x] Implementasi client auth serta UI; jalankan test dan build GREEN.

### Task 4: Integrasi dan dokumentasi
Files: compose.yaml, .env.example, .gitignore, README.md, migration/README.md, docs/implementation/auth-status.md.
- [x] Pisahkan environment antar service dan selaraskan port publik 8989.
- [x] Dokumentasikan konfigurasi runtime role, konfirmasi email, generate/apply dan pengujian.
- [x] Jalankan pemeriksaan yang tersedia dan review implementasi; perbaiki slow-body deadline. Build auth-service, DB dan Docker/Chrome tetap belum terverifikasi; lihat status.
- [x] Simpan status faktual dan sinkronkan artefak Obsidian.

