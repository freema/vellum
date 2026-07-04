import { useEffect, useState, type FormEvent } from 'react'
import { LogoMark } from '../components/Logo'
import { GithubMark } from '../components/Icon'
import type { ApiClient } from '../lib/api'

type ClientKey = 'code' | 'desktop' | 'chatgpt' | 'cursor'

const CLAUDE_STEPS: [string, string][] = [
  ['Settings → Connectors', 'Open Settings → Connectors in claude.ai.'],
  ['Add custom connector', 'Scroll down and click “Add custom connector”.'],
  ['Paste the endpoint', 'Into “Remote MCP server URL”, then confirm “Add”.'],
  ['Authorize', 'Sign in and pick your vault tools — Vellum shows up among your tools.'],
]

const TAB_ORDER: ClientKey[] = ['code', 'desktop', 'chatgpt', 'cursor']

/** Connect/login screen — design artboard 1a (two-column connect card). */
export default function Connect({ api, onConnected }: { api: ApiClient; onConnected: () => void }) {
  const [secret, setSecret] = useState('')
  const [show, setShow] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [clientTab, setClientTab] = useState<ClientKey>('code')
  const [copied, setCopied] = useState<'endpoint' | 'client' | null>(null)
  const [stars, setStars] = useState<number | null>(null)

  const endpoint = `${window.location.origin}/mcp`

  const clients: Record<ClientKey, { label: string; lang: string; cmd: string; note: string }> = {
    code: {
      label: 'Claude Code',
      lang: 'terminal',
      cmd: `claude mcp add --transport http vellum ${endpoint}`,
      note: 'Run in a terminal. On the first tool call a browser opens for OAuth sign-in.',
    },
    desktop: {
      label: 'Claude Desktop',
      lang: 'claude_desktop_config.json',
      cmd: `{\n  "mcpServers": {\n    "vellum": {\n      "type": "http",\n      "url": "${endpoint}"\n    }\n  }\n}`,
      note: 'Add to your config file, then restart Claude Desktop.',
    },
    chatgpt: {
      label: 'ChatGPT',
      lang: 'Settings → Connectors → Add',
      cmd: endpoint,
      note: 'Paste as a custom connector URL. Requires a plan with connectors / developer mode.',
    },
    cursor: {
      label: 'Cursor',
      lang: '~/.cursor/mcp.json',
      cmd: `{\n  "mcpServers": {\n    "vellum": {\n      "url": "${endpoint}"\n    }\n  }\n}`,
      note: 'Add to your Cursor MCP config, then reload the window.',
    },
  }

  useEffect(() => {
    let alive = true
    fetch('https://api.github.com/repos/freema/vellum')
      .then((r) => (r.ok ? r.json() : null))
      .then((d) => {
        if (alive && d && typeof d.stargazers_count === 'number') setStars(d.stargazers_count)
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [])

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

  const copy = (which: 'endpoint' | 'client', text: string) => {
    void navigator.clipboard?.writeText(text)
    setCopied(which)
    window.setTimeout(() => setCopied((c) => (c === which ? null : c)), 1600)
  }

  const cur = clients[clientTab]

  return (
    <div className="connect-page">
      <div className="connect2">
        <div className="connect2__head">
          <LogoMark size={40} variant="paper" surface="var(--bg)" />
          <div className="connect2__head-text">
            <div className="connect2__title">Connect to Vellum</div>
            <div className="connect2__sub">Open the web vault, or point an MCP client at it.</div>
          </div>
          <span className="connect2__spacer" />
          <a
            className="connect2__star"
            href="https://github.com/freema/vellum"
            target="_blank"
            rel="noopener"
            title="Star Vellum on GitHub"
          >
            <GithubMark size={15} />
            <span className="connect2__star-label">Star on GitHub</span>
            {stars != null && (
              <span className="connect2__star-count">
                <span className="connect2__star-glyph">★</span>
                {stars}
              </span>
            )}
          </a>
        </div>

        <div className="connect2__body">
          <form className="connect2__signin" onSubmit={submit}>
            <div className="connect2__signin-title">Sign in</div>
            <div className="connect2__signin-sub">Open the web vault with your client secret.</div>

            <label className="connect2__label">Client secret</label>
            <div className="connect2__input-row">
              <svg
                className="connect2__key"
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
                className="connect2__input"
                type={show ? 'text' : 'password'}
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
                autoFocus
                placeholder="paste your secret"
              />
              <span className="connect2__show" onClick={() => setShow(!show)}>
                {show ? 'hide' : 'show'}
              </span>
            </div>

            {error && <div className="connect2__error">{error}</div>}

            <button className="connect2__connect" type="submit" disabled={busy || !secret.trim()}>
              {busy ? 'Connecting…' : 'Connect'}
            </button>

            <div className="connect2__signin-spacer" />
            <div className="connect2__signin-foot">
              No secret yet? An MCP client authorizes itself over OAuth — start on the right.
            </div>
          </form>

          <div className="connect2__guide">
            <div className="connect2__guide-head">
              <span className="connect2__guide-title">Connect a client</span>
              <span className="connect2__badge">~2 min</span>
            </div>

            <div className="connect2__endpoint">
              <span className="connect2__endpoint-label">endpoint</span>
              <span className="connect2__endpoint-url">{endpoint}</span>
              <button className="connect2__copy" onClick={() => copy('endpoint', endpoint)}>
                {copied === 'endpoint' ? 'Copied ✓' : 'Copy'}
              </button>
            </div>

            <div className="connect2__claude">
              <div className="connect2__claude-avatar">C</div>
              <span className="connect2__claude-name">Claude.ai (web)</span>
              <span className="connect2__badge connect2__badge--rec">recommended</span>
            </div>
            <div className="connect2__steps">
              {CLAUDE_STEPS.map(([title, desc], i) => (
                <div className="connect2__step" key={title}>
                  <div className="connect2__step-num">{i + 1}</div>
                  <div className="connect2__step-text">
                    <div className="connect2__step-title">{title}</div>
                    <div className="connect2__step-desc">{desc}</div>
                  </div>
                </div>
              ))}
            </div>

            <div className="connect2__others">
              <div className="connect2__others-title">Other clients</div>
              <div className="connect2__tabs">
                {TAB_ORDER.map((k) => (
                  <span
                    key={k}
                    className={`connect2__tab${clientTab === k ? ' connect2__tab--active' : ''}`}
                    onClick={() => setClientTab(k)}
                  >
                    {clients[k].label}
                  </span>
                ))}
              </div>
              <div className="connect2__code">
                <div className="connect2__code-head">
                  <span className="connect2__code-lang">{cur.lang}</span>
                  <button className="connect2__copy" onClick={() => copy('client', cur.cmd)}>
                    {copied === 'client' ? 'Copied ✓' : 'Copy'}
                  </button>
                </div>
                <pre className="connect2__code-body">{cur.cmd}</pre>
              </div>
              <div className="connect2__note">{cur.note}</div>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
