# LiveTranslate — Operator Runbook

## Scope

Private deployment for fewer than three users. Every artifact remains owner-only. The application does not provide team sharing. Production activation still requires real Supabase, Chrome unpacked, Deepgram, and Sumopod validation.

## Deployment order

1. Keep the existing Supabase project and backups. Apply migrations in order: `0001`, `0002`, `0003`, `0004`, `0005`, `0006`, `0007`, `0008`, `0009`, `0010`, `0011`.
2. Verify RLS with `sh migration/tests/check-meetings.sh` locally, then inspect policies and grants on the target project. Never configure a service-role/secret key in the application.
3. Enable Supabase Storage and confirm bucket `meeting-documents` is private. Confirm authenticated policies restrict object prefixes to `auth.uid()`.
4. Enable Supabase Cron. Confirm `livetranslate-staging-retention` is active and the historical `livetranslate-transcript-retention` job is inactive. Cron records private-object deletion in `storage_cleanup_jobs`; it does not mutate `storage.objects` directly.
5. Configure an HTTPS Supabase URL plus the Supabase publishable key, Deepgram key/model/language, and Sumopod key/model only in backend environment. Plain HTTP Supabase endpoints are accepted only for loopback development. Expose backend through HTTPS. Do not expose container port directly to the Internet.
6. Build with `docker compose build`, start, then require `/health` = 200 and `/ready` = 200. Readiness validates configuration without making paid provider requests.
7. Build extension from a clean checkout. Distribute `extension/dist` privately through managed-browser policy or a restricted Chrome Web Store listing. Never package `.env`, source maps containing secrets, or backend credentials.

## Private-object cleanup worker

Run the one-shot worker at least every 15 minutes from the host scheduler:

```sh
docker compose run --rm --no-deps --entrypoint /app/storage-cleanup \
  -e SUPABASE_SECRET_KEY backend
```

Inject `SUPABASE_SECRET_KEY` from the host secret manager. Never place it in the long-running backend service, Compose file, extension, image, or repository. The worker is the only component allowed to use a Supabase secret/service-role key: it reads pending `storage_cleanup_jobs`, deletes through the supported Storage API, then marks jobs complete. Failed deletions remain pending with `attempts` and `last_error`; reruns are idempotent. Alert if the oldest pending job exceeds 30 minutes or attempts continue increasing. Verify after discard, 24-hour expiry, and archive delete that the matching job becomes `completed` and the private object is absent.

## Required real-service gates

- Chrome unpacked: supported meeting URL produces a connection offer without opening popup; **Nanti** creates no occurrence; **Terhubung** then explicit **Mulai merekam** captures tab audio and never requests microphone permission.
- Lifecycle: stop, tab close, navigation, provider terminal error, and four-hour cap all finish the occurrence and expose the post-meeting decision.
- Language matrix: run representative English, Indonesian, and actual team source languages. Record Deepgram language detection, Indonesian translation accuracy, unsupported/code-switched inputs, and p95 final-to-translation latency. Do not claim “all languages.”
- Documents: upload representative text PDF, scanned PDF/OCR, DOCX, XLSX with cached formulas, and CSV. Confirm encrypted PDF and malformed/oversized archives fail explicitly.
- Assistance: verify all representative questions, including questions addressed to other participants. Every answered item must cite the uploaded document/version/locator; absent evidence must produce `-` in both languages.
- Concurrency: run three simultaneous meetings. Required targets: translation p95 ≤3 seconds and Q/A p95 ≤8 seconds; no cross-owner or cross-occurrence result.
- Archive: save, read detail, and export ZIP; compare every manifest SHA-256/count and every available source document SHA-256. A `partial_failed` archive may contain `source-unavailable.json` only for a document already marked failed. Delete explicitly and verify metadata is purged, the archive has a retained `deleted` tombstone/manifest, the cleanup job completes, and the private object is gone. Discard and 24-hour expiry must affect staging only.

## Backup and restore

Back up the database and private `meeting-documents` bucket as one consistency set. A usable backup records database snapshot time, object inventory, object SHA-256, migration version, and encryption/key metadata. Encrypt backup media and restrict it to operators.

Restore into an isolated project first. Apply schema to the recorded version, restore database rows, restore private objects to their exact manifest paths, then verify manifest counts/hashes under an owner test account. Restore is an operator-controlled consistency-set/PITR operation; there is intentionally no owner-facing restore endpoint because a manifest contains hashes and counts, not deleted content. A deleted archive must not be reconstructed from staging or an unrelated newer backup. Production cutover requires an explicit operator decision and audit record.

Provider/operator backups and logs may retain data outside application deletion guarantees. State that limit to users; do not describe explicit delete as cryptographic erasure of provider backups.

## Key rotation

1. Create the replacement key at Supabase/Deepgram/Sumopod.
2. Update backend secret storage and restart one instance.
3. Run `/ready`; explicitly run provider checks only during a maintenance window because they make paid requests.
4. Verify one authenticated owner-only request and one denied cross-owner request.
5. Revoke the old key. Search logs/build artifacts for accidental value exposure; rotate again if exposed.

Never rotate to a Supabase service-role key. JWT/session revocation and provider key rotation are separate operations.

## Monitoring and incident response

Backend logs contain trace ID, method, path, status, and duration only; query strings, JWTs, prompts, transcript/document contents, and provider bodies must not be logged. Alert on readiness failure, HTTP 429/5xx, staging cleanup failures, storage growth, translation/Q&A latency, provider refusal, and archive integrity mismatch.

On suspected owner-isolation failure: disable external access, preserve metadata-only evidence, revoke sessions/keys, identify affected occurrence IDs, and notify affected owners. Do not copy document/transcript content into tickets or logs.

## Rollback

Roll back the application image without dropping new tables or re-enabling the old 30-day transcript purge. Keep staging cleanup active. Database rollback is forward-fix only unless a separately tested restore is approved. Never restore discarded staging or deleted archives as an automatic rollback side effect.
