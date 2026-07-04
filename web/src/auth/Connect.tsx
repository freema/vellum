import { useState, type FormEvent } from 'react'
import { LogoMark } from '../components/Logo'
import type { ApiClient } from '../lib/api'

/** Connect/login screen — design artboard 1a (440px card). */
export default function Connect({ api, onConnected }: { api: ApiClient; onConnected: () => void }) {
  const [secret, setSecret] = useState('')
  const [show, setShow] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.connect(secret.trim())
      onConnected()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Connection failed')
    } finally {
      setBusy(false)
    }
  }

  const host = window.location.host

  return (
    <div className="connect-page">
      <form className="connect-card" onSubmit={submit}>
        <LogoMark size={52} variant="paper" surface="var(--bg)" />
        <div className="connect-card__wordmark">vellum</div>
        <div className="connect-card__subtitle">Connect to your vault</div>

        <div className="connect-card__field">
          <label className="v-label">Client secret</label>
          <div className="connect-card__input-row">
            <svg
              className="connect-card__key"
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <circle cx="7.5" cy="15.5" r="4.5" />
              <path d="m11 12 10-10" />
              <path d="m16 7 3 3" />
            </svg>
            <input
              className="connect-card__input"
              type={show ? 'text' : 'password'}
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
              autoFocus
              placeholder="paste your secret"
            />
            <button type="button" className="connect-card__show" onClick={() => setShow(!show)}>
              {show ? 'hide' : 'show'}
            </button>
          </div>
        </div>

        {error && <div className="connect-card__error">{error}</div>}

        <button className="connect-card__button" type="submit" disabled={busy || !secret.trim()}>
          {busy ? 'Connecting…' : 'Connect'}
        </button>

        <div className="connect-card__footer">
          <div className="connect-card__hint">Or register this host with the agent</div>
          <div className="connect-card__snippet">
            claude mcp add --transport http \<br />
            &nbsp;&nbsp;vellum https://{host}/mcp
          </div>
        </div>
      </form>
    </div>
  )
}
