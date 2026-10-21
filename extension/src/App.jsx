import { useEffect, useState } from 'react'
import { API_BASE_URL } from './config.js'
import { createPopupAuth, extensionRequest } from './extension-client.js'
import { TRANSCRIPT_STATUS, transcriptLines, transcriptNotice } from './transcript-state.js'

const auth = createPopupAuth(API_BASE_URL)
export default function App() {
  const [mode, setMode] = useState('login')
  const [account, setAccount] = useState(null)
  const [pending, setPending] = useState(true)
  const [message, setMessage] = useState('Memeriksa sesi…')
  const [error, setError] = useState(false)
  const [backend, setBackend] = useState('')
  const [checking, setChecking] = useState(false)
  const [meeting, setMeeting] = useState(null)
  const [capture, setCapture] = useState(null)
  const [transcript, setTranscript] = useState(null)
  // The popup shows the finals in order and the partial that is still running.
  const lines = transcriptLines(transcript)
  // The reason of a failure is only shown when it says more than the status of the transcript.
  const notice = transcriptNotice(transcript)

  useEffect(() => {
    if (!globalThis.chrome?.runtime?.id) return
    let active = true
    const changed = (changes, area) => {
      if (active && area === 'session' && changes.meeting_status) setMeeting(changes.meeting_status.newValue ?? null)
    }
    chrome.storage.onChanged.addListener(changed)
    extensionRequest('meeting.inspect').then(value => { if (active && value) setMeeting(value) }).catch(() => { if (active) setMeeting({ status: 'sync_error' }) })
    return () => { active = false; chrome.storage.onChanged.removeListener(changed) }
  }, [account?.user?.id])

  async function retryMeeting() {
    setMeeting(value => ({ ...value, status: 'syncing' }))
    try { const value = await extensionRequest('meeting.retry'); if (value) setMeeting(value) }
    catch { setMeeting({ status: 'sync_error' }) }
  }

  useEffect(() => {
    if (!globalThis.chrome?.runtime?.id) return
    let active = true
    const changed = (changes, area) => {
      if (active && area === 'session' && changes.audio_status) setCapture(changes.audio_status.newValue ?? null)
    }
    chrome.storage.onChanged.addListener(changed)
    extensionRequest('audio.status').then(value => { if (active) setCapture(value ?? null) }).catch(() => {})
    return () => { active = false; chrome.storage.onChanged.removeListener(changed) }
  }, [account?.user?.id])

  // The transcript is written by the document offscreen while the capture runs.
  useEffect(() => {
    if (!globalThis.chrome?.runtime?.id) return
    let active = true
    const changed = (changes, area) => {
      if (active && area === 'session' && changes.transcript_state) setTranscript(changes.transcript_state.newValue ?? null)
    }
    chrome.storage.onChanged.addListener(changed)
    chrome.storage.session.get('transcript_state').then(values => { if (active) setTranscript(values?.transcript_state ?? null) }).catch(() => {})
    return () => { active = false; chrome.storage.onChanged.removeListener(changed) }
  }, [account?.user?.id])

  async function startCapture() {
    setCapture(value => ({ ...(value ?? {}), status: 'starting' }))
    try { const value = await extensionRequest('audio.start'); if (value) setCapture(value) }
    catch (err) { setCapture({ status: 'error', message: err.message }) }
  }

  async function stopCapture() {
    try { const value = await extensionRequest('audio.stop'); if (value) setCapture(value) }
    catch (err) { setCapture({ status: 'error', message: err.message }) }
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
    try { await auth.logout() } catch (err) { setMessage(err.message); setError(true) }
    finally { setAccount(null); setPending(false) }
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
      </section>}
      {account && meeting?.status === 'registered' && <section className="card" aria-label="Capture audio">
        <span className="eyebrow">CAPTURE AUDIO</span>
        <p role="status" aria-live="polite">{{ starting: 'Menyiapkan capture…', capturing: 'Merekam audio tab meeting.', stopped: 'Capture dihentikan.', error: capture?.message ?? 'Capture gagal.' }[capture?.status] ?? 'Belum merekam. Audio tidak disimpan setelah dikirim.'}</p>
        {capture?.run_id && <p className="meeting-id">Run: {capture.run_id}</p>}
        {capture?.chunks !== undefined && <p className="meeting-id">{capture.chunks} chunk · {capture.bytes} byte</p>}
        {capture?.status === 'capturing'
          ? <button className="secondary" onClick={stopCapture}>Berhenti merekam</button>
          : <button onClick={startCapture}>Mulai merekam</button>}
      </section>}
      {account && <section className="card" aria-label="Transkrip">
        <span className="eyebrow">TRANSKRIP</span>
        <p role="status" aria-live="polite">{TRANSCRIPT_STATUS[transcript?.status] ?? 'Belum ada transkrip.'}</p>
        {notice && <p className="notice error" role="alert">{notice}</p>}
        {lines.map((text, index) => (
          <p key={index} className={transcript?.partial?.text && index === lines.length - 1 ? 'notice' : undefined}>{text}</p>
        ))}
      </section>}
      {message && <p className={error ? 'notice error' : 'notice'} role={error ? 'alert' : 'status'} aria-live="polite">{message}</p>}
      {!account && error && <button className="text-button" disabled={pending} onClick={restore}>Pulihkan sesi</button>}
      <footer><button className="text-button" disabled={checking} onClick={checkBackend}>Periksa Backend</button><p role="status" aria-live="polite">{backend}</p></footer>
    </main>
  )
}
