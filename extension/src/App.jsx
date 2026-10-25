import { useEffect, useMemo, useRef, useState } from 'react'
import { API_BASE_URL } from './config.js'
import { createPopupAuth, extensionRequest } from './extension-client.js'
import { TRANSCRIPT_STATUS, transcriptNotice } from './transcript-state.js'
import { createSavedTranscripts } from './saved-transcripts.js'

const auth = createPopupAuth(API_BASE_URL)
const ANSWER_STATUS_NOTICE = {
  no_evidence: 'Jawaban tidak tersedia dari dokumen.',
  parse_failed: 'Dokumen gagal diproses; jawaban belum tersedia.',
  retrieval_failed: 'Pencarian bukti dokumen gagal.',
  provider_failed: 'Penyedia jawaban gagal.',
  quota_exceeded: 'Kuota penyedia jawaban habis.',
}
export default function App() {
  const [mode, setMode] = useState('login')
  const [account, setAccount] = useState(null)
  const [pending, setPending] = useState(true)
  const [message, setMessage] = useState('Memeriksa sesi…')
  const [error, setError] = useState(false)
  const [backend, setBackend] = useState('')
  const [checking, setChecking] = useState(false)
  const [rawMeeting, setMeeting] = useState(null)
  const [rawCapture, setCapture] = useState(null)
  const [rawTranscript, setTranscript] = useState(null)
  const [occurrence, setOccurrence] = useState(null)
  const [translationState, setTranslationState] = useState(null)
  const [documents, setDocuments] = useState([])
  const [documentBusy, setDocumentBusy] = useState(false)
  const [questions, setQuestions] = useState([])
  const [archives, setArchives] = useState([])
  const [archiveBusy, setArchiveBusy] = useState(false)
  const [archiveCursor, setArchiveCursor] = useState(null)
  const [archiveDetail, setArchiveDetail] = useState(null)
  const [savedState, setSavedState] = useState(null)
  const ownerRef = useRef(null)
  const meetingRef = useRef(null)
  const ownerId = account?.user?.id ?? null
  ownerRef.current = ownerId
  const saved = useMemo(() => createSavedTranscripts({ request: extensionRequest, onChange: setSavedState }), [])
  const meeting = !rawMeeting?.user_id || rawMeeting.user_id === ownerId ? rawMeeting : null
  const meetingId = ownerId && meeting?.status === 'registered' ? meeting.meeting_id : null
  const capture = rawCapture?.owner_id === ownerId && ownerId ? rawCapture : null
  const transcript = rawTranscript?.owner_id === ownerId && ownerId && rawTranscript.run_id === capture?.run_id ? rawTranscript : null
  const archive = savedState?.owner_id === ownerId && savedState?.meeting_id === meetingId ? savedState : null
  const translations = translationState?.owner_id === ownerId && translationState?.run_id === capture?.run_id ? translationState.translations ?? [] : []
  const translationsBySeq = new Map(translations.map(item => [item.source_seq, item]))
  // The reason of a failure is only shown when it says more than the status of the transcript.
  const notice = transcriptNotice(transcript)

  useEffect(() => {
    saved.setContext(ownerId, meetingId)
    if (ownerId && meetingId) saved.list()
    return () => saved.setContext(null, null)
  }, [ownerId, meetingId, saved])

  useEffect(() => {
    if (ownerId && meetingId && capture?.run_id) saved.list()
  }, [ownerId, meetingId, capture?.run_id, saved])

  useEffect(() => {
    if (!ownerId || !archive?.selected || archive.selected !== capture?.run_id || !capture?.save_transcript || (archive.storage?.state ?? capture.transcript_state) !== 'saving') return
    const timer = setInterval(() => {
      const current = saved.getState()
      if (!current.loading && !current.exporting && !current.deleting) saved.read(false, { preserveTimeline: true })
    }, 3000)
    return () => clearInterval(timer)
  }, [ownerId, archive?.selected, archive?.storage?.state, capture?.run_id, capture?.save_transcript, capture?.transcript_state, saved])

  useEffect(() => {
    if (!globalThis.chrome?.runtime?.id) return
    const changed = (changes, area) => {
      if (area !== 'session') return
      if ((changes.auth_session && !changes.auth_session.newValue) || (changes.meeting_account && changes.meeting_account.newValue?.id !== ownerRef.current)) {
        ownerRef.current = null
        saved.setContext(null, null)
        setAccount(null); setCapture(null); setTranscript(null); setMeeting(null); setOccurrence(null); setTranslationState(null)
      }
    }
    chrome.storage.onChanged.addListener(changed)
    return () => { saved.setContext(null, null); chrome.storage.onChanged.removeListener(changed) }
  }, [saved])

  useEffect(() => {
    if (!globalThis.chrome?.runtime?.id) return
    let active = true
    const owner = ownerId
    const receive = value => {
      if (!active || ownerRef.current !== owner) return
      if (value?.status === 'syncing' && meetingRef.current?.status === 'registered' && value.tab_id === meetingRef.current.tab_id && value.canonical_url === meetingRef.current.canonical_url && meetingRef.current.user_id === owner) return
      const next = value?.user_id && value.user_id !== owner ? null : value
      saved.setContext(owner, next?.status === 'registered' ? next.meeting_id : null)
      setMeeting(next)
      meetingRef.current = next
    }
    const changed = (changes, area) => {
      if (area === 'session' && changes.meeting_status) receive(changes.meeting_status.newValue ?? null)
    }
    chrome.storage.onChanged.addListener(changed)
    extensionRequest('meeting.inspect').then(receive).catch(() => receive({ status: 'sync_error' }))
    return () => { active = false; chrome.storage.onChanged.removeListener(changed) }
  }, [account?.user?.id])

  async function retryMeeting() {
    const owner = ownerRef.current
    setMeeting(value => ({ ...value, status: 'syncing' }))
    try { const value = await extensionRequest('meeting.retry'); if (ownerRef.current === owner && value) setMeeting(value) }
    catch { if (ownerRef.current === owner) setMeeting({ status: 'sync_error' }) }
  }

  useEffect(() => {
    if (!globalThis.chrome?.runtime?.id) return
    let active = true
    const changed = (changes, area) => {
      if (active && ownerRef.current === ownerId && area === 'session' && changes.audio_status) setCapture(changes.audio_status.newValue ?? null)
    }
    chrome.storage.onChanged.addListener(changed)
    extensionRequest('audio.status').then(value => { if (active && ownerRef.current === ownerId) setCapture(value ?? null) }).catch(() => {})
    return () => { active = false; chrome.storage.onChanged.removeListener(changed) }
  }, [account?.user?.id])

  useEffect(() => {
    if (!globalThis.chrome?.runtime?.id) return
    let active = true
    const changed = (changes, area) => {
      if (active && area === 'session' && changes.meeting_occurrence) setOccurrence(changes.meeting_occurrence.newValue ?? null)
    }
    chrome.storage.onChanged.addListener(changed)
    chrome.storage.session.get('meeting_occurrence').then(values => {
      if (active) setOccurrence(values?.meeting_occurrence ?? null)
    }).catch(() => {})
    return () => { active = false; chrome.storage.onChanged.removeListener(changed) }
  }, [account?.user?.id])

  // The transcript is written by the document offscreen while the capture runs.
  useEffect(() => {
    if (!globalThis.chrome?.runtime?.id) return
    let active = true
    const changed = (changes, area) => {
      if (active && ownerRef.current === ownerId && area === 'session' && changes.transcript_state) setTranscript(changes.transcript_state.newValue ?? null)
    }
    chrome.storage.onChanged.addListener(changed)
    chrome.storage.session.get('transcript_state').then(values => { if (active && ownerRef.current === ownerId) setTranscript(values?.transcript_state ?? null) }).catch(() => {})
    return () => { active = false; chrome.storage.onChanged.removeListener(changed) }
  }, [account?.user?.id])

  useEffect(() => {
    if (!ownerId || !capture?.run_id) return
    let active = true
    const refresh = () => extensionRequest('translation.current.read').then(value => {
      if (active && value?.owner_id === ownerId && value.run_id === capture.run_id) setTranslationState(value)
    }).catch(() => {})
    refresh()
    const timer = setInterval(refresh, 1000)
    return () => { active = false; clearInterval(timer) }
  }, [ownerId, capture?.run_id])

  useEffect(() => {
    if (!occurrence?.id) { setDocuments([]); return }
    let active = true
    extensionRequest('documents.list').then(value => { if (active) setDocuments(value?.documents ?? []) }).catch(() => {})
    return () => { active = false }
  }, [occurrence?.id])

  useEffect(() => {
    if (!occurrence?.id) { setQuestions([]); return }
    let active = true
    const refresh = () => extensionRequest('questions.current.read').then(value => {
      if (active) setQuestions(value?.questions ?? [])
    }).catch(() => {})
    refresh()
    const timer = setInterval(refresh, 1500)
    return () => { active = false; clearInterval(timer) }
  }, [occurrence?.id])

  async function refreshArchives(cursor = null) {
    try {
      const value = await extensionRequest('archives.list', cursor ? { cursor } : {})
      const rows = value?.archives ?? []
      setArchives(current => cursor ? [...current, ...rows.filter(row => !current.some(item => item.id === row.id))] : rows)
      setArchiveCursor(value?.next_cursor ?? null)
    } catch (err) {
      setMessage(err.message)
      setError(true)
    }
  }
  async function readArchiveDetail(id) {
    setArchiveBusy(true)
    try { setArchiveDetail(await extensionRequest('archive.detail', { id })) }
    catch (err) { setMessage(err.message); setError(true) }
    finally { setArchiveBusy(false) }
  }
  async function exportArchive(id) {
    setArchiveBusy(true)
    try {
      const value = await extensionRequest('archive.export', { id })
      const url = URL.createObjectURL(new Blob([value.bytes], { type: value.type }))
      const link = document.createElement('a'); link.href = url; link.download = value.name; link.click()
      URL.revokeObjectURL(url)
    } catch (err) {
      setMessage(err.message)
      setError(true)
    } finally { setArchiveBusy(false) }
  }
  async function deleteArchive(id) {
    setArchiveBusy(true)
    try { await extensionRequest('archive.delete', { id }); await refreshArchives() }
    catch (err) { setMessage(err.message); setError(true) }
    finally { setArchiveBusy(false) }
  }
  useEffect(() => {
    if (!ownerId) { setArchives([]); setArchiveCursor(null); setArchiveDetail(null); return }
    refreshArchives()
  }, [ownerId, occurrence?.status])

  async function uploadDocument(event) {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    setDocumentBusy(true); setError(false)
    try {
      await extensionRequest('document.upload', { name: file.name, type: file.type, bytes: await file.arrayBuffer() })
      const value = await extensionRequest('documents.list')
      setDocuments(value?.documents ?? [])
    } catch (err) {
      setMessage(err.message); setError(true)
    } finally { setDocumentBusy(false) }
  }

  async function connectOccurrence() {
    const owner = ownerRef.current
    try {
      const value = await extensionRequest('occurrence.connect')
      if (ownerRef.current === owner && value?.occurrence) setOccurrence(value.occurrence)
    } catch (err) {
      if (ownerRef.current === owner) { setMessage(err.message); setError(true) }
    }
  }

  async function dismissOffer() {
    const value = await extensionRequest('meeting.offer.dismiss')
    if (value) setMeeting(value)
  }

  async function startCapture() {
    const owner = ownerRef.current
    setCapture(value => ({ ...(value ?? {}), owner_id: owner, status: 'starting' }))
    try { const value = await extensionRequest('audio.start'); if (ownerRef.current === owner && value) setCapture(value) }
    catch (err) { if (ownerRef.current === owner) setCapture({ owner_id: owner, status: 'error', message: err.message }) }
  }

  async function stopCapture() {
    const owner = ownerRef.current
    try { const value = await extensionRequest('audio.stop'); if (ownerRef.current === owner && value) setCapture(value) }
    catch (err) { if (ownerRef.current === owner) setCapture(value => ({ ...(value ?? {}), owner_id: owner, status: 'stop_failed', message: err.message })) }
  }

  async function decideArchive(save) {
    const owner = ownerRef.current
    try {
      const value = await extensionRequest(save ? 'occurrence.archive' : 'occurrence.discard')
      if (ownerRef.current === owner && value?.occurrence) setOccurrence(value.occurrence)
    } catch (err) {
      if (ownerRef.current === owner) { setMessage(err.message); setError(true) }
    }
  }

  useEffect(() => {
    let active = true
    auth.restore().then(value => {
      if (active) { setAccount(value); setMessage('') }
    }).catch(err => {
      if (active) { setMessage(err.message); setError(true) }
    }).finally(() => { if (active) setPending(false) })
    return () => { active = false }
  }, [])

  async function submit(event) {
    event.preventDefault()
    const form = event.currentTarget
    const fields = new FormData(form)
    const email = String(fields.get('email')).trim()
    const password = String(fields.get('password'))
    const display_name = String(fields.get('display_name') ?? '').trim()
    setError(false)
    if ([...password].length < 8 || [...password].length > 128) {
      setMessage('Password harus 8–128 karakter.'); setError(true); return
    }
    if (mode === 'register' && (password !== fields.get('confirm_password') || !display_name || [...display_name].length > 100)) {
      setMessage('Periksa nama dan pastikan konfirmasi password sama.'); setError(true); return
    }
    setPending(true); setMessage('Memproses…')
    try {
      if (mode === 'login') { setAccount(await auth.login({ email, password })); setMessage('') }
      else {
        const result = await auth.register({ display_name, email, password })
        if (result.account) { setAccount(result.account); setMessage('') }
        else { setMode('login'); setMessage(result.message) }
      }
      form.reset()
    } catch (err) { setMessage(err.message); setError(true) }
    finally { setPending(false) }
  }
  async function logout() {
    setPending(true); setError(false); setMessage('')
    try { await auth.logout() }
    catch (err) { setMessage(err.message); setError(true) }
    finally {
      setAccount(null); ownerRef.current = null
      saved.setContext(null, null)
      setCapture(null); setTranscript(null); setMeeting(null); setOccurrence(null)
      setPending(false)
    }
  }
  async function restore() {
    setPending(true); setError(false)
    try { setAccount(await auth.restore()); setMessage('') }
    catch (err) { setMessage(err.message); setError(true) }
    finally { setPending(false) }
  }
  async function checkBackend() {
    setChecking(true); setBackend('Memeriksa…')
    try {
      const response = await fetch(`${API_BASE_URL}/health`, { signal: AbortSignal.timeout(10000) })
      const result = await response.json()
      if (!response.ok || result.status !== 'ok' || result.service !== 'livetranslate-backend') throw new Error()
      setBackend('Backend terhubung.')
    } catch { setBackend('Gagal terhubung. Periksa backend dan alamat API.') }
    finally { setChecking(false) }
  }
  return (
    <main>
      <header><span className="mark" aria-hidden="true">LT</span><div><h1>LiveTranslate</h1><p className="subtitle">Asisten rapat Anda</p></div></header>
      {account ? (
        <section className="card" aria-label="Akun">
          <span className="eyebrow">AKUN AKTIF</span>
          <h2>{account.profile.display_name}</h2><p className="email">{account.user.email}</p>
          <button className="secondary" disabled={pending} onClick={logout}>Keluar</button>
        </section>
      ) : (
        <section className="card">
          <div className="tabs" aria-label="Autentikasi">
            <button aria-pressed={mode === 'login'} disabled={pending} onClick={() => { setMode('login'); setMessage(''); setError(false) }}>Masuk</button>
            <button aria-pressed={mode === 'register'} disabled={pending} onClick={() => { setMode('register'); setMessage(''); setError(false) }}>Daftar</button>
          </div>
          <h2>{mode === 'login' ? 'Masuk ke akun Anda' : 'Buat akun baru'}</h2>
          <form key={mode} onSubmit={submit}>
            <fieldset disabled={pending}>
              {mode === 'register' && <label>Nama<input name="display_name" autoComplete="name" required maxLength={200} /></label>}
              <label>Email<input name="email" type="email" autoComplete="email" required maxLength={254} /></label>
              <label>Password<input name="password" type="password" autoComplete={mode === 'login' ? 'current-password' : 'new-password'} required maxLength={256} /><small>8–128 karakter</small></label>
              {mode === 'register' && <label>Konfirmasi password<input name="confirm_password" type="password" autoComplete="new-password" required maxLength={256} /></label>}
              <button type="submit">{pending ? 'Memproses…' : mode === 'login' ? 'Masuk' : 'Buat akun'}</button>
            </fieldset>
          </form>
          <p className="hint">Sesi berakhir saat browser ditutup.</p>
        </section>
      )}
      {account && <section className="card" aria-label="Meeting">
        <span className="eyebrow">MEETING TAB AKTIF</span>
        <p role="status" aria-live="polite">{{ unsupported: 'Tab ini bukan halaman meeting yang didukung.', no_active_tab: 'Tidak ada tab Chrome yang difokuskan.', login_required: 'Masuk kembali untuk menyinkronkan meeting.', syncing: 'Menyinkronkan meeting…', registered: 'Sesi meeting tersimpan.', sync_error: 'Meeting belum tersinkron. Akan dicoba kembali otomatis.' }[meeting?.status] ?? 'Memeriksa tab…'}</p>
        {meeting?.canonical_url && <p className="meeting-link">{meeting.canonical_url}</p>}
        {meeting?.meeting_id && <p className="meeting-id">ID sesi: {meeting.meeting_id}</p>}
        {meeting?.status === 'sync_error' && <button className="secondary" onClick={retryMeeting}>Coba sinkronkan</button>}
        {meeting?.status === 'registered' && !meeting.offer_dismissed && !['pending_documents', 'active', 'awaiting_decision'].includes(occurrence?.status) && <div className="connection-offer" role="group" aria-label="Tawaran koneksi meeting">
          <p>Meeting terdeteksi: {meeting.provider}. Terhubung ke LiveTranslate?</p>
          <button onClick={connectOccurrence}>Terhubung</button>
          <button className="secondary" onClick={dismissOffer}>Nanti</button>
        </div>}
      </section>}
      {account && meeting?.status === 'registered' && ['pending_documents', 'active', 'awaiting_decision'].includes(occurrence?.status) && <section className="card" aria-label="Capture audio">
        <span className="eyebrow">CAPTURE AUDIO</span>
        <p role="status" aria-live="polite">{{ starting: 'Menyiapkan capture…', capturing: 'Merekam audio tab meeting.', stopped: 'Capture dihentikan.', stop_failed: capture?.message ?? 'Capture belum berhasil dihentikan. Coba lagi.', error: capture?.message ?? 'Capture gagal.' }[capture?.status] ?? 'Belum merekam. Audio tidak disimpan setelah dikirim.'}</p>
        {capture?.run_id && <p className="meeting-id">Run: {capture.run_id}</p>}
        {capture?.chunks !== undefined && <p className="meeting-id">{capture.chunks} chunk · {capture.bytes} byte</p>}
        {capture?.run_id && <p className="storage-choice">Catatan meeting disimpan privat sementara sampai keputusan setelah meeting. Audio mentah tidak disimpan.</p>}
        {capture?.save_transcript && <p role="status">{storageLabel(capture.storage?.state ?? capture.transcript_state)}</p>}
        {['capturing', 'stop_failed'].includes(capture?.status)
          ? <button className="secondary" onClick={stopCapture}>Berhenti merekam</button>
          : <button disabled={capture?.status === 'starting'} onClick={startCapture}>Mulai merekam</button>}
        {occurrence?.status === 'awaiting_decision' && <div className="archive-decision" role="group" aria-label="Keputusan penyimpanan meeting">
          <p>Simpan hasil meeting beserta catatan, jawaban, dokumen, dan kutipan?</p>
          <button onClick={() => decideArchive(true)}>Simpan</button>
          <button className="secondary" onClick={() => decideArchive(false)}>Tidak</button>
        </div>}
      </section>}
      {account && occurrence?.id && ['pending_documents', 'active'].includes(occurrence.status) && <section className="card" aria-label="Dokumen meeting">
        <span className="eyebrow">DOKUMEN MEETING</span>
        <label>Unggah PDF, DOCX, XLSX, atau CSV
          <input type="file" accept=".pdf,.docx,.xlsx,.csv,application/pdf,application/vnd.openxmlformats-officedocument.wordprocessingml.document,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,text/csv" disabled={documentBusy} onChange={uploadDocument} />
        </label>
        <small>Maksimum 10 file, 20 MiB per file, dan 100 MiB per meeting.</small>
        {documentBusy && <p role="status">Mengunggah dan mengekstrak dokumen…</p>}
        <ul>{documents.map(document => <li key={document.id}>{document.file_name} · {document.status}</li>)}</ul>
      </section>}
      {account && occurrence?.id && <section className="card" aria-label="Pertanyaan dan jawaban">
        <span className="eyebrow">PERTANYAAN &amp; JAWABAN</span>
        {!questions.length && <p>Belum ada pertanyaan yang terdeteksi.</p>}
        <ol>{questions.map(question => {
          const answer = question.answers?.[0]
          return <li key={question.id}>
            <p>{question.text}</p>
            <p lang="id">{answer?.answer_id ?? 'Menyiapkan jawaban…'}</p>
            <p lang="en">{answer?.answer_en ?? 'Preparing answer…'}</p>
            {answer?.status && answer.status !== 'answered' && <p className="notice" role="status">{ANSWER_STATUS_NOTICE[answer.status] ?? 'Jawaban tidak tersedia.'}</p>}
            {!!answer?.citations?.length && <ul>{answer.citations.map((citation, index) => <li key={`${question.id}-${index}`}>{citation.file_name} v{citation.document_version} · {JSON.stringify(citation.locator)}</li>)}</ul>}
          </li>
        })}</ol>
      </section>}
      {account && <section className="card" aria-label="Arsip meeting">
        <span className="eyebrow">ARSIP MEETING</span>
        <button className="text-button" disabled={archiveBusy} onClick={() => refreshArchives()}>Segarkan arsip</button>
        {!archives.length && <p>Belum ada arsip permanen.</p>}
        <ul>{archives.map(item => <li key={item.id}>
          <p>{formatDate(item.committed_at)} · {item.occurrence_id}</p>
          <button disabled={archiveBusy} onClick={() => readArchiveDetail(item.id)}>Lihat detail</button>
          <button disabled={archiveBusy} onClick={() => exportArchive(item.id)}>Ekspor ZIP lengkap</button>
          <button className="secondary danger" disabled={archiveBusy} onClick={() => deleteArchive(item.id)}>Hapus arsip</button>
        </li>)}</ul>
        {archiveCursor && <button className="secondary" disabled={archiveBusy} onClick={() => refreshArchives(archiveCursor)}>Muat arsip sebelumnya</button>}
        {archiveDetail?.archive && <div className="archive-detail">
          <p>Occurrence: {archiveDetail.archive.occurrence_id} · status {archiveDetail.archive.status}</p>
          <p>Manifest schema: {archiveDetail.archive.manifest?.schema_version}</p>
          <ul>{(archiveDetail.documents ?? []).map(document => <li key={document.id}>{document.file_name} · {document.status}</li>)}</ul>
        </div>}
      </section>}
      {account && <section className="card" aria-label="Transkrip">
        <span className="eyebrow">TRANSKRIP</span>
        <label className="transcript-selector">Tampilan
          <select value={archive?.selected ?? ''} onChange={event => { saved.select(event.target.value || null); if (event.target.value) saved.read() }}>
            <option value="">Live · sesi sementara</option>
            {archive?.selected && !archive.runs.some(run => run.id === archive.selected) && <option value={archive.selected}>Run dipilih · {archive.selected}</option>}
            {(archive?.runs ?? []).map(run => <option key={run.id} value={run.id}>{formatDate(run.started_at)} · {run.id.slice(0, 8)}</option>)}
          </select>
        </label>
        {meetingId && <button className="text-button" disabled={archive?.listing} onClick={() => saved.list()}>Segarkan run tersimpan</button>}
        {archive?.listing && <p role="status">Memuat run tersimpan…</p>}
        {meetingId && !archive?.listing && !archive?.runs.length && <p>Belum ada run tersimpan yang tersedia.</p>}
        {archive?.next_cursor && <button className="secondary" disabled={archive.listing} onClick={() => saved.list(true)}>Run sebelumnya</button>}
        {archive?.error && <p className="notice error" role="alert">{archive.error}</p>}
        {archive?.selected ? <>
          <h2>Timeline tersimpan</h2>
          <p role="status">{archive.loading ? 'Memuat timeline…' : storageLabel(archive.storage?.state)}</p>
          <button className="text-button" disabled={archive.loading || archive.deleting} onClick={() => saved.read()}>Segarkan timeline</button>
          {archive.storage?.expires_at && <p className="meeting-id">Kedaluwarsa: {formatDate(archive.storage.expires_at)}</p>}
          {archive.run && <p className="meeting-id">Capture: {formatDate(archive.run.started_at)} · {archive.run.status}</p>}
          {archive.storage && !archive.storage.complete && <p className="notice">Arsip belum lengkap. Ekspor hanya berisi snapshot final yang sudah tersimpan, bukan jaminan seluruh percakapan.</p>}
          {!archive.loading && archive.storage && !archive.segments.length && <p>Belum ada segmen final yang tersedia.</p>}
          <ol className="timeline">{archive.segments.map(segment => <li key={segment.seq}><time>{formatTime(segment.start)}</time><p>{segment.text}</p></li>)}</ol>
          {archive.next_after_seq !== null && <button className="secondary" disabled={archive.loading || archive.deleting} onClick={() => saved.read(true)}>Muat segmen berikutnya</button>}
          <div className="archive-actions">
            <button className="secondary" disabled={archive.exporting || unavailable(archive.storage)} onClick={() => saved.exportJSON()}>{archive.exporting ? 'Menyiapkan seluruh snapshot…' : 'Ekspor JSON lengkap'}</button>
          </div>
          <small>Penghapusan permanen dilakukan pada seluruh arsip meeting melalui kartu Arsip Meeting, bukan per run.</small>
        </> : <>
          <p className="hint">Live hanya untuk sesi browser; bukan arsip tersimpan.</p>
          <p role="status" aria-live="polite">{TRANSCRIPT_STATUS[transcript?.status] ?? 'Belum ada transkrip.'}</p>
          {notice && <p className="notice error" role="alert">{notice}</p>}
          {(transcript?.finals ?? []).map(segment => {
            const translated = translationsBySeq.get(segment.seq)
            return <div key={segment.seq} className="translation-pair">
              <p>{segment.text}</p>
              <p lang="id" className={translated?.status === 'failed' ? 'notice error' : undefined}>{translated?.status === 'success' ? translated.text : translated?.status === 'failed' ? 'Terjemahan tidak tersedia.' : 'Menerjemahkan…'}</p>
            </div>
          })}
          {transcript?.partial?.text && <p className="notice">{transcript.partial.text}</p>}
        </>}
      </section>}
      {message && <p className={error ? 'notice error' : 'notice'} role={error ? 'alert' : 'status'} aria-live="polite">{message}</p>}
      {!account && error && <button className="text-button" disabled={pending} onClick={restore}>Pulihkan sesi</button>}
      <footer><button className="text-button" disabled={checking} onClick={checkBackend}>Periksa Backend</button><p role="status" aria-live="polite">{backend}</p></footer>
    </main>
  )
}

function storageLabel(state) {
  return { disabled: 'Penyimpanan tidak diaktifkan.', saving: 'Penyimpanan berlangsung · snapshot belum lengkap.', saved: 'Transkrip tersimpan lengkap.', failed: 'Penyimpanan gagal · arsip tidak lengkap.', incomplete: 'Arsip tidak lengkap; writer sudah tidak aktif.', deleted: 'Transkrip telah dihapus. Teks live hanya sesi sementara.', expired: 'Transkrip telah kedaluwarsa.' }[state] ?? 'Pilih run untuk membaca arsip.'
}
function unavailable(storage) { return !storage || ['disabled', 'deleted', 'expired'].includes(storage.state) }
function formatDate(value) { return value ? new Date(value).toLocaleString('id-ID') : '—' }
function formatTime(value) {
  const seconds = Math.max(0, Math.floor(value ?? 0))
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}
