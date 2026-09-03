import { useEffect, useState } from 'react'
import { fetchPublicNote, ShareGoneError, type PublicNote } from '../lib/api'
import { MarkdownView } from '../components/markdown'
import { LogoMark } from '../components/Logo'
import { Icon } from '../components/Icon'
import { usePersistedState } from '../lib/usePersistedState'

/**
 * The page a share link opens. It is the vault seen from outside: one note,
 * read-only, no session, no navigation into anything else. Everything the
 * workspace offers — the tree, search, the editor, other notes — is absent by
 * construction, not hidden, because this page only ever holds one document.
 *
 * It renders inside the same bundle as the workspace but outside its router
 * and outside the auth gate: a reader has no token and must never be asked
 * for one.
 */
export default function SharePage({ token }: { token: string }) {
  const [note, setNote] = useState<PublicNote | null>(null)
  const [failure, setFailure] = useState<'notfound' | 'gone' | 'error' | null>(null)
  // The reader gets the same theme control as the owner. It writes the same
  // key, which is harmless: they are different browsers.
  const [theme, setTheme] = usePersistedState<'light' | 'dark'>('vellum_theme', () =>
    window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light',
  )

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
  }, [theme])

  useEffect(() => {
    let cancelled = false
    void fetchPublicNote(token)
      .then((n) => {
        if (cancelled) return
        setNote(n)
        document.title = `${n.title} — vellum`
      })
      .catch((err) => {
        if (cancelled) return
        if (err instanceof ShareGoneError) {
          setFailure(err.status === 410 ? 'gone' : 'notfound')
        } else {
          setFailure('error')
        }
      })
    return () => {
      cancelled = true
    }
  }, [token])

  if (failure) return <ShareFailure kind={failure} />
  if (!note) return <div className="sh-page" />

  return (
    <div className="sh-page">
      <header className="sh-topbar">
        <span className="sh-topbar__brand">
          <LogoMark size={24} variant="paper" surface="var(--bg)" />
          <span className="sh-topbar__wordmark">vellum</span>
        </span>
        <span className="sh-topbar__spacer" />
        <a className="sh-btn" href={`/s/${encodeURIComponent(token)}/raw`}>
          <Icon name="download" size={14} />
          Download .md
        </a>
        <button
          className="sh-icon-btn"
          onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
          title="Toggle midnight ink"
        >
          <Icon name="moon" size={15} />
        </button>
      </header>

      <main className="sh-sheet">
        <div className="sh-sheet__label">Shared note</div>
        <h1 className="sh-sheet__title">{note.title}</h1>
        <div className="sh-sheet__meta">
          <span>updated {note.updated} ago</span>
          <span className="sh-sheet__sep">·</span>
          <span>
            {note.words} {note.words === 1 ? 'word' : 'words'}
          </span>
          {note.expires && (
            <>
              <span className="sh-sheet__sep">·</span>
              <span className="sh-sheet__expiry">link expires in {note.expires}</span>
            </>
          )}
          {(note.tags ?? []).map((t) => (
            <span key={t} className="sh-tag">
              #{t}
            </span>
          ))}
        </div>
        <div className="sh-sheet__rule" />
        <MarkdownView body={note.body} />
      </main>

      <footer className="sh-foot">
        <span>
          A read-only view of one note. It always shows the current version, and the person who
          shared it can revoke this link at any time.
        </span>
        <a href="https://github.com/freema/vellum" target="_blank" rel="noreferrer noopener">
          Made with vellum
        </a>
      </footer>
    </div>
  )
}

const FAILURES = {
  notfound: {
    code: '404',
    label: 'HTTP 404 · Not found',
    title: 'This link doesn’t work',
    body: 'It may have been revoked, or it may have expired. Ask whoever shared it with you for a new one.',
  },
  gone: {
    code: '410',
    label: 'HTTP 410 · Gone',
    title: 'This note is gone',
    body: 'The note behind this link was deleted. The link itself was valid.',
  },
  error: {
    code: '500',
    label: 'HTTP 500 · Error',
    title: 'This note could not be loaded',
    body: 'Something went wrong on the server holding it. Trying again in a moment may work.',
  },
} as const

function ShareFailure({ kind }: { kind: keyof typeof FAILURES }) {
  const f = FAILURES[kind]
  return (
    <div className="sh-page sh-page--center">
      <div className="sh-fail">
        <div className="sh-fail__code">{f.code}</div>
        <div className="sh-fail__label">{f.label}</div>
        <div className="sh-fail__title">{f.title}</div>
        <div className="sh-fail__body">{f.body}</div>
        {kind === 'error' && (
          <button className="v-btn v-btn--primary" onClick={() => window.location.reload()}>
            Try again
          </button>
        )}
      </div>
    </div>
  )
}
