import { useEffect, useState } from 'react'
import { API_BASE_URL } from './config.js'
import { createAuthClient } from './auth.js'

const auth = createAuthClient({ baseUrl: API_BASE_URL })
export default function App() {
  const [mode, setMode] = useState('login')
  const [account, setAccount] = useState(null)
  const [pending, setPending] = useState(true)
  const [message, setMessage] = useState('Memeriksa sesi…')
  const [error, setError] = useState(false)
  const [backend, setBackend] = useState('')
  const [checking, setChecking] = useState(false)

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
      {message && <p className={error ? 'notice error' : 'notice'} role={error ? 'alert' : 'status'} aria-live="polite">{message}</p>}
      {!account && error && <button className="text-button" disabled={pending} onClick={restore}>Pulihkan sesi</button>}
      <footer><button className="text-button" disabled={checking} onClick={checkBackend}>Periksa Backend</button><p role="status" aria-live="polite">{backend}</p></footer>
    </main>
  )
}
