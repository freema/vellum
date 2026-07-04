import { useEffect, useMemo, useState } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useNavigate, useParams } from 'react-router-dom'
import { ApiClient } from './lib/api'
import Connect from './auth/Connect'
import Workspace from './workspace/Workspace'
import Preview from './dev/Preview'

type Phase = 'loading' | 'connect' | 'ready'

export default function App() {
  const api = useMemo(() => new ApiClient(), [])
  const [phase, setPhase] = useState<Phase>('loading')
  const [version, setVersion] = useState('dev')

  useEffect(() => {
    api.onAuthError = () => setPhase('connect')
    void api.health().then(async ({ auth, version }) => {
      setVersion(version)
      if (!auth) {
        setPhase('ready')
        return
      }
      // A refresh restores the session token — verify it before re-prompting.
      if (api.hasToken()) {
        try {
          await api.version()
          setPhase('ready')
          return
        } catch {
          /* token expired/invalid — fall through to connect */
        }
      }
      setPhase('connect')
    })
  }, [api])

  if (phase === 'loading') return null
  if (phase === 'connect') return <Connect api={api} onConnected={() => setPhase('ready')} />

  return (
    <BrowserRouter>
      <Routes>
        <Route path="/dev/components" element={<Preview />} />
        <Route path="/wl/:target" element={<WikilinkResolver api={api} />} />
        <Route path="/n/*" element={<Workspace api={api} version={version} />} />
        <Route path="*" element={<Workspace api={api} version={version} />} />
      </Routes>
    </BrowserRouter>
  )
}

/** Resolves a [[wikilink]] target to a note path by filename stem. */
function WikilinkResolver({ api }: { api: ApiClient }) {
  const { target = '' } = useParams()
  const navigate = useNavigate()
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    void api.listNotes().then((notes) => {
      const want = decodeURIComponent(target).toLowerCase()
      const match =
        notes.find((n) => stem(n.path) === want) ??
        notes.find((n) => n.title.toLowerCase() === want) ??
        notes.find((n) => n.path.toLowerCase() === want || n.path.toLowerCase() === `${want}.md`)
      if (match) navigate(`/n/${match.path}`, { replace: true })
      else setFailed(true)
    })
  }, [api, target, navigate])

  if (failed) return <Navigate to="/" replace />
  return null
}

function stem(path: string): string {
  const base = path.split('/').pop() ?? path
  return base.replace(/\.(md|markdown)$/i, '').toLowerCase()
}
