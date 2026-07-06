import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  ApiClient,
  ConflictError,
  AuthError,
  NotFoundError,
  relativeAge,
  type Note,
  type NoteEntry,
  type SearchResult,
  type TagCount,
  type Connection,
  type ConnectionsData,
  type ActivityData,
  type ActivityEvent,
  type Notification,
} from '../lib/api'
import { CommandPalette, PaletteItem, Highlight } from '../components/palette'
import { LogoMark } from '../components/Logo'
import { Icon, GithubMark, type IconName } from '../components/Icon'

type TypeFilter = 'all' | 'task' | 'knowledge'
type ViewMode = 'edit' | 'split' | 'preview'
type Toast = { text: string; tone: 'ok' | 'danger' }

const STATUS: Record<string, { label: string; dot: string; soft: string; text: string }> = {
  backlog: {
    label: 'Backlog',
    dot: 'var(--status-backlog)',
    soft: 'var(--status-backlog-soft)',
    text: 'var(--status-backlog-text)',
  },
  'in-progress': {
    label: 'In progress',
    dot: 'var(--status-inprogress)',
    soft: 'var(--status-inprogress-soft)',
    text: 'var(--status-inprogress-text)',
  },
  done: {
    label: 'Done',
    dot: 'var(--status-done)',
    soft: 'var(--status-done-soft)',
    text: 'var(--status-done-text)',
  },
}
const STATUS_ORDER = ['backlog', 'in-progress', 'done'] as const

const TOUR = [
  {
    id: 'tourSearch',
    place: 'below-left',
    title: 'Find anything',
    body: 'Search across every note and #tag. Press ⌘K from anywhere in the vault.',
  },
  {
    id: 'tourTree',
    place: 'right',
    title: 'Your vault',
    body: 'Folders live here. Click one to open it, or drag a note onto a folder to move it.',
  },
  {
    id: 'tourNewFolder',
    place: 'right',
    title: 'New folder',
    body: 'Add a folder with the ＋ button — it nests under whatever folder you have selected.',
  },
  {
    id: 'tourList',
    place: 'right',
    title: 'Notes & filters',
    body: 'Notes in the open folder. Filter by task / knowledge and by status.',
  },
  {
    id: 'tourToolbar',
    place: 'below-left',
    title: 'Format markdown',
    body: 'Bold, headings, lists, checkboxes and links — or just type the syntax by hand.',
  },
  {
    id: 'tourModes',
    place: 'below-right',
    title: 'Edit · Split · Preview',
    body: 'Write raw markdown, see it rendered, or keep both side by side.',
  },
  {
    id: 'notifBtn',
    place: 'below-right',
    title: 'Notifications',
    body: 'Curator suggestions, overdue tasks and new MCP sessions land here.',
  },
  {
    id: 'activityBtn',
    place: 'below-right',
    title: 'Activity & curator',
    body: 'A live log of what the curator agent and connected clients did to your vault.',
  },
  {
    id: 'tourStar',
    place: 'below-right',
    title: 'Open source',
    body: 'Vellum is open source — star it on GitHub if you find it useful.',
  },
  {
    id: 'tourHelp',
    place: 'below-right',
    title: 'Help anytime',
    body: 'Reopen this tour and the markdown cheatsheet from here whenever you need it.',
  },
] as const

function noteType(e: NoteEntry): 'task' | 'knowledge' {
  return e.type === 'task' ? 'task' : 'knowledge'
}
function basename(path: string): string {
  return path.split('/').pop() ?? path
}
function dirname(path: string): string {
  const i = path.lastIndexOf('/')
  return i < 0 ? '' : path.slice(0, i)
}
// friendly label for the currently selected directory (for placeholders)
function dirLabel(dir: string): string {
  if (!dir) return 'the vault'
  const leaf = dir.split('/').pop() ?? dir
  return leaf.charAt(0).toUpperCase() + leaf.slice(1)
}

export default function Workspace({ api, version }: { api: ApiClient; version: string }) {
  const navigate = useNavigate()
  const params = useParams()
  const selectedPath = params['*'] || ''

  const [entries, setEntries] = useState<NoteEntry[]>([])
  const [tags, setTags] = useState<TagCount[]>([])
  const [selectedDir, setSelectedDir] = useState('')
  const [typeFilter, setTypeFilter] = useState<TypeFilter>('all')
  const [statusFilter, setStatusFilter] = useState('')
  const [activeTags, setActiveTags] = useState<string[]>([])
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [helpOpen, setHelpOpen] = useState(false)
  const [theme, setTheme] = useState<'light' | 'dark'>('light')
  const [toast, setToast] = useState<Toast | null>(null)
  const [saving, setSaving] = useState(false)
  const [projectsOpen, setProjectsOpen] = useState(true)
  const [dragPath, setDragPath] = useState<string | null>(null)
  const [dropDir, setDropDir] = useState<string | null>(null)
  const [tourStep, setTourStep] = useState<number | null>(null)
  const [folders, setFolders] = useState<string[]>([])
  const [addingFolder, setAddingFolder] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [folderToDelete, setFolderToDelete] = useState<string | null>(null)

  // top-bar panels + their data
  const [connOpen, setConnOpen] = useState(false)
  const [connData, setConnData] = useState<ConnectionsData | null>(null)
  const [notifOpen, setNotifOpen] = useState(false)
  const [notifs, setNotifs] = useState<Notification[]>([])
  const [unread, setUnread] = useState(0)
  const [activityOpen, setActivityOpen] = useState(false)
  const [activityData, setActivityData] = useState<ActivityData | null>(null)
  const [activityFilter, setActivityFilter] = useState<'all' | 'mcp' | 'curator' | 'errors'>('all')
  const [activitySearch, setActivitySearch] = useState('')
  const [errorCount, setErrorCount] = useState(0)
  const [starCount, setStarCount] = useState<number | null>(null)

  const rootRef = useRef<HTMLDivElement>(null)
  const toastTimer = useRef<number | undefined>(undefined)

  const refresh = useCallback(async () => {
    try {
      const [notes, tagList, dirs] = await Promise.all([
        api.listNotes(),
        api.tags(),
        api.listFolders().catch(() => [] as string[]),
      ])
      setEntries(notes)
      setTags(tagList)
      setFolders(dirs)
    } catch (err) {
      if (!(err instanceof AuthError)) console.error(err)
    }
  }, [api])

  useEffect(() => {
    void refresh()
  }, [refresh])

  // Continuous vault scan: MCP clients write to the vault behind the SPA's
  // back, so the tree/list silently re-syncs every 30 s and whenever the tab
  // regains focus. The manual re-scan button stays for impatient moments.
  useEffect(() => {
    const id = window.setInterval(() => {
      if (!document.hidden) void refresh()
    }, 30_000)
    const onFocus = () => void refresh()
    window.addEventListener('focus', onFocus)
    return () => {
      window.clearInterval(id)
      window.removeEventListener('focus', onFocus)
    }
  }, [refresh])

  // Server-down detection (deploy restart): any failed API call flips the
  // reconnect overlay on; /healthz polling flips it back off.
  const [offline, setOffline] = useState(false)
  const [retryIn, setRetryIn] = useState(4)
  useEffect(() => {
    api.onUnavailable = () => setOffline(true)
    return () => {
      api.onUnavailable = undefined
    }
  }, [api])
  const probeHealth = useCallback(async () => {
    try {
      const res = await fetch('/healthz')
      if (res.ok) {
        setOffline(false)
        void refresh()
        return true
      }
    } catch {
      /* still down */
    }
    return false
  }, [refresh])
  useEffect(() => {
    if (!offline) return
    setRetryIn(4)
    const id = window.setInterval(() => {
      setRetryIn((s) => {
        if (s > 1) return s - 1
        void probeHealth()
        return 4
      })
    }, 1000)
    return () => window.clearInterval(id)
  }, [offline, probeHealth])

  const showToast = useCallback((text: string, tone: 'ok' | 'danger' = 'ok') => {
    window.clearTimeout(toastTimer.current)
    setToast({ text, tone })
    toastTimer.current = window.setTimeout(() => setToast(null), 2400)
  }, [])

  // Manual vault re-scan — picks up notes added/changed via MCP without a full
  // page reload (otherwise the only way to see them).
  const doRefresh = useCallback(async () => {
    setRefreshing(true)
    try {
      await refresh()
      showToast('Vault re-scanned')
    } finally {
      setRefreshing(false)
    }
  }, [refresh, showToast])

  // notification + error badges on load
  useEffect(() => {
    api
      .notifications()
      .then((n) => {
        setNotifs(n.notifications)
        setUnread(n.unread)
      })
      .catch(() => {})
    api
      .activity('all')
      .then((d) => setErrorCount(d.errorCount))
      .catch(() => {})
  }, [api])

  // GitHub star count — best effort, silently ignored offline / rate-limited
  useEffect(() => {
    let alive = true
    fetch('https://api.github.com/repos/freema/vellum')
      .then((r) => (r.ok ? r.json() : null))
      .then((d) => {
        if (alive && d && typeof d.stargazers_count === 'number') setStarCount(d.stargazers_count)
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [])

  // ---- top-bar panels ----
  const openConnections = useCallback(() => {
    setConnOpen(true)
    api.connections().then(setConnData).catch(() => {})
  }, [api])
  const loadActivity = useCallback(
    (f: 'all' | 'mcp' | 'curator' | 'errors') => {
      api
        .activity(f)
        .then((d) => {
          setActivityData(d)
          setErrorCount(d.errorCount)
        })
        .catch(() => {})
    },
    [api],
  )
  const openActivity = useCallback(() => {
    setActivityOpen(true)
    setNotifOpen(false)
    loadActivity(activityFilter)
  }, [loadActivity, activityFilter])
  const openNotifications = useCallback(() => {
    setNotifOpen((o) => !o)
    api
      .notifications()
      .then((n) => {
        setNotifs(n.notifications)
        setUnread(n.unread)
      })
      .catch(() => {})
  }, [api])
  const revokeConn = useCallback(
    async (id: string) => {
      try {
        await api.revokeConnection(id)
        setConnData(await api.connections())
        showToast('Session revoked', 'danger')
      } catch (err) {
        if (!(err instanceof AuthError)) showToast('Revoke failed', 'danger')
      }
    },
    [api, showToast],
  )
  const runCurator = useCallback(async () => {
    try {
      const r = await api.runCurator()
      setActivityData(await api.activity(activityFilter))
      showToast(
        r.enabled ? `Curator ran — ${r.changes} suggestion${r.changes === 1 ? '' : 's'}` : 'Curator is off',
      )
    } catch (err) {
      if (!(err instanceof AuthError)) showToast('Curator run failed', 'danger')
    }
  }, [api, activityFilter, showToast])
  const changeActivityFilter = useCallback(
    (f: 'all' | 'mcp' | 'curator' | 'errors') => {
      setActivityFilter(f)
      loadActivity(f)
    },
    [loadActivity],
  )
  const markAllRead = useCallback(() => {
    setNotifs((ns) => ns.map((n) => ({ ...n, read: true })))
    setUnread(0)
  }, [])
  const dismissNotif = useCallback((id: string) => {
    setNotifs((ns) => ns.filter((n) => n.id !== id))
    setUnread((u) => Math.max(0, u - 1))
  }, [])

  const toggleTheme = () => {
    const next = theme === 'light' ? 'dark' : 'light'
    setTheme(next)
    document.documentElement.setAttribute('data-theme', next)
  }

  // ---- keyboard: ⌘K palette, Escape closes overlays ----
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'K')) {
        e.preventDefault()
        setPaletteOpen((o) => !o)
        return
      }
      if (e.key === 'Escape') {
        setPaletteOpen(false)
        setHelpOpen(false)
        setTourStep(null)
        setConnOpen(false)
        setNotifOpen(false)
        setActivityOpen(false)
        setAddingFolder(false)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [])

  // ---- onboarding tour: auto-start once per browser ----
  const startTour = useCallback(() => {
    setHelpOpen(false)
    setPaletteOpen(false)
    setTourStep(0)
  }, [])
  useEffect(() => {
    let seen = false
    try {
      seen = !!localStorage.getItem('vellum_onboarded')
    } catch {
      /* private mode */
    }
    if (seen) return
    const t = window.setTimeout(() => setTourStep(0), 700)
    return () => window.clearTimeout(t)
  }, [])
  const endTour = useCallback(() => {
    try {
      localStorage.setItem('vellum_onboarded', '1')
    } catch {
      /* ignore */
    }
    setTourStep(null)
  }, [])

  // ---- derived tree data ----
  const inDir = useCallback(
    (e: NoteEntry, dir: string) => dir === '' || e.path.startsWith(dir + '/'),
    [],
  )
  const counts = useMemo(() => {
    const byDir = new Map<string, number>()
    for (const e of entries) {
      const parts = e.path.split('/')
      for (let i = 1; i <= parts.length - 1; i++) {
        const dir = parts.slice(0, i).join('/')
        byDir.set(dir, (byDir.get(dir) ?? 0) + 1)
      }
    }
    return byDir
  }, [entries])

  // per-folder counts of notes matching the active tag filter — powers the
  // "1/6" badges shown while one or more tags are selected.
  const matchCounts = useMemo(() => {
    const byDir = new Map<string, number>()
    if (activeTags.length === 0) return byDir
    for (const e of entries) {
      if (!activeTags.every((t) => e.tags?.includes(t))) continue
      const parts = e.path.split('/')
      for (let i = 1; i <= parts.length - 1; i++) {
        const dir = parts.slice(0, i).join('/')
        byDir.set(dir, (byDir.get(dir) ?? 0) + 1)
      }
    }
    return byDir
  }, [entries, activeTags])

  // Directories come from the real folder list (so empty folders show too);
  // note paths are a fallback if the folders endpoint is unavailable.
  const dirSet = useMemo(() => {
    const set = new Set<string>(folders)
    for (const d of counts.keys()) set.add(d)
    return set
  }, [folders, counts])

  const projectDirs = useMemo(
    () => [...dirSet].filter((d) => /^projects\/[^/]+$/.test(d)).sort(),
    [dirSet],
  )

  const otherTopDirs = useMemo(() => {
    const dirs = new Set<string>()
    for (const d of dirSet) {
      if (d.includes('/')) continue
      if (d !== 'inbox' && d !== 'projects' && d !== 'archive') dirs.add(d)
    }
    return [...dirs].sort()
  }, [dirSet])

  const allDirs = useMemo(() => {
    const set = new Set<string>(['inbox', 'projects', 'archive', ...dirSet])
    return [...set].sort()
  }, [dirSet])

  const listEntries = useMemo(() => {
    return entries
      .filter((e) => inDir(e, selectedDir))
      .filter((e) => typeFilter === 'all' || noteType(e) === typeFilter)
      .filter((e) => statusFilter === '' || e.status === statusFilter)
      .filter((e) => activeTags.every((t) => e.tags?.includes(t)))
      .sort((a, b) => b.modTime - a.modTime)
  }, [entries, selectedDir, typeFilter, statusFilter, activeTags, inDir])

  const openNote = useCallback(
    (path: string) => {
      setPaletteOpen(false)
      navigate(`/n/${path}`)
    },
    [navigate],
  )

  const createNote = async () => {
    const dir = selectedDir || 'inbox'
    let name = 'untitled.md'
    for (let i = 2; entries.some((e) => e.path === `${dir}/${name}`); i++) {
      name = `untitled-${i}.md`
    }
    const path = `${dir}/${name}`
    try {
      await api.putNote(path, '# Untitled\n')
      await refresh()
      openNote(path)
      showToast(`New note in ${dir.split('/').pop()}`)
    } catch (err) {
      if (!(err instanceof AuthError)) showToast('Could not create note', 'danger')
    }
  }

  // ---- path-level actions ----
  const moveToDir = useCallback(
    async (from: string, dir: string) => {
      if (dirname(from) === dir) return
      const to = `${dir}/${basename(from)}`
      try {
        await api.moveNote(from, to)
        await refresh()
        if (from === selectedPath) navigate(`/n/${to}`, { replace: true })
        showToast(`Moved to ${dir.split('/').pop()}`)
      } catch (err) {
        if (!(err instanceof AuthError)) showToast('Move failed', 'danger')
      } finally {
        setDragPath(null)
        setDropDir(null)
      }
    },
    [api, refresh, selectedPath, navigate, showToast],
  )

  const renameNote = useCallback(
    async (from: string, rawName: string) => {
      let name = rawName.trim()
      if (!name) return
      if (!/\.(md|markdown)$/i.test(name)) name += '.md'
      const to = dirname(from) ? `${dirname(from)}/${name}` : name
      if (to === from) return
      try {
        await api.moveNote(from, to)
        await refresh()
        if (from === selectedPath) navigate(`/n/${to}`, { replace: true })
        showToast('Renamed')
      } catch (err) {
        if (!(err instanceof AuthError)) showToast('Rename failed', 'danger')
      }
    },
    [api, refresh, selectedPath, navigate, showToast],
  )

  const deleteNote = useCallback(
    async (path: string) => {
      try {
        await api.deleteNote(path)
        await refresh()
        if (path === selectedPath) navigate('/', { replace: true })
        showToast('Note deleted', 'danger')
      } catch (err) {
        if (!(err instanceof AuthError)) showToast('Delete failed', 'danger')
      }
    },
    [api, refresh, selectedPath, navigate, showToast],
  )

  const deleteFolder = useCallback(
    async (dir: string) => {
      setFolderToDelete(null)
      try {
        const r = await api.deleteFolder(dir)
        await refresh()
        if (selectedPath === dir || selectedPath.startsWith(dir + '/')) navigate('/', { replace: true })
        if (selectedDir === dir || selectedDir.startsWith(dir + '/')) setSelectedDir('')
        showToast(`Folder deleted${r.notes ? ` · ${r.notes} note${r.notes === 1 ? '' : 's'}` : ''}`, 'danger')
      } catch (err) {
        if (!(err instanceof AuthError)) showToast('Could not delete folder', 'danger')
      }
    },
    [api, refresh, selectedPath, selectedDir, navigate, showToast],
  )

  const addFolder = useCallback(
    async (rawName: string) => {
      setAddingFolder(false)
      const name = rawName.trim().replace(/^\/+|\/+$/g, '')
      if (!name) return
      const path = selectedDir ? `${selectedDir}/${name}` : name
      try {
        await api.createFolder(path)
        await refresh()
        if (selectedDir === 'projects' || selectedDir.startsWith('projects/')) setProjectsOpen(true)
        setSelectedDir(path)
        showToast(`Folder “${name}” created`)
      } catch (err) {
        if (!(err instanceof AuthError)) showToast('Could not create folder', 'danger')
      }
    },
    [api, refresh, selectedDir, showToast],
  )

  return (
    <div className="ws" ref={rootRef}>
      <TopBar
        version={version}
        activeTags={activeTags}
        onRemoveTag={(t) => setActiveTags(activeTags.filter((x) => x !== t))}
        onOpenPalette={() => setPaletteOpen(true)}
        onToggleTheme={toggleTheme}
        onOpenHelp={() => setHelpOpen(true)}
        connActiveCount={connData?.activeCount ?? 0}
        onOpenConnections={openConnections}
        starCount={starCount}
        unreadCount={unread}
        onOpenNotifications={openNotifications}
        onOpenActivity={openActivity}
        errorCount={errorCount}
      />
      <div className="ws-body">
        <TreePanel
          counts={counts}
          matchCounts={matchCounts}
          filtering={activeTags.length > 0}
          projectDirs={projectDirs}
          otherTopDirs={otherTopDirs}
          selectedDir={selectedDir}
          onSelectDir={setSelectedDir}
          projectsOpen={projectsOpen}
          onToggleProjects={() => setProjectsOpen((o) => !o)}
          addingFolder={addingFolder}
          selectedLabel={dirLabel(selectedDir)}
          onStartAddFolder={() => setAddingFolder(true)}
          onAddFolder={addFolder}
          onCancelAddFolder={() => setAddingFolder(false)}
          refreshing={refreshing}
          onRefresh={doRefresh}
          onDeleteFolder={setFolderToDelete}
          tags={tags}
          activeTags={activeTags}
          onToggleTag={(t) =>
            setActiveTags(
              activeTags.includes(t) ? activeTags.filter((x) => x !== t) : [...activeTags, t],
            )
          }
          totalNotes={entries.length}
          dropDir={dropDir}
          dragging={dragPath !== null}
          onDropDir={(dir) => dragPath && moveToDir(dragPath, dir)}
          onDragOverDir={setDropDir}
        />
        <ListPanel
          entries={listEntries}
          selectedDir={selectedDir}
          selectedPath={selectedPath}
          typeFilter={typeFilter}
          statusFilter={statusFilter}
          onTypeFilter={setTypeFilter}
          onStatusFilter={setStatusFilter}
          onOpen={openNote}
          onHover={(p) => api.prefetchNote(p)}
          onCreate={createNote}
          onDragStart={setDragPath}
          onDragEnd={() => {
            setDragPath(null)
            setDropDir(null)
          }}
        />
        <EditorPanel
          key={selectedPath}
          api={api}
          path={selectedPath}
          allDirs={allDirs}
          onSaved={refresh}
          onSavingChange={setSaving}
          onMove={moveToDir}
          onRename={renameNote}
          onDelete={deleteNote}
          onOpenPalette={() => setPaletteOpen(true)}
          onBack={() => navigate('/')}
          showToast={showToast}
        />
      </div>
      <footer className="ws-statusbar">
        {selectedPath ? (
          <span>
            <span className="ws-statusbar__dir">
              {selectedPath.includes('/')
                ? selectedPath.slice(0, selectedPath.lastIndexOf('/') + 1)
                : ''}
            </span>
            <span className="ws-statusbar__file">{basename(selectedPath)}</span>
          </span>
        ) : (
          <span className="ws-statusbar__dir">{selectedDir || 'vault'}</span>
        )}
        <span className="ws-statusbar__spacer" />
        <span>{entries.length} notes</span>
        <span className="ws-statusbar__sep">·</span>
        <span>md</span>
        {selectedPath && (
          <>
            <span className="ws-statusbar__sep">·</span>
            <span className={saving ? 'ws-statusbar__dirty' : 'ws-statusbar__saved'}>
              {saving ? 'Saving…' : 'Saved ✓'}
            </span>
          </>
        )}
      </footer>
      {paletteOpen && (
        <PaletteOverlay api={api} onClose={() => setPaletteOpen(false)} onOpen={openNote} />
      )}
      {offline && (
        <div className="ws-overlay-scrim">
          <div className="ws-err ws-err--card">
            <div className="ws-err__code ws-err__code--warn">503</div>
            <div className="ws-err__label ws-err__label--warn">HTTP 503 · Unavailable</div>
            <div className="ws-err__title">Vellum is restarting</div>
            <div className="ws-err__body">
              The server is coming back up after an update. This usually takes a few seconds — the
              page will reconnect on its own.
            </div>
            <div className="ws-err__pill">
              <span className="ws-err__pill-dot" />
              retrying in {retryIn}s…
            </div>
            <div className="ws-err__actions">
              <button className="v-btn v-btn--primary" onClick={() => void probeHealth()}>
                Reconnect now
              </button>
            </div>
          </div>
        </div>
      )}
      {toast && (
        <div className="ws-toast">
          <span
            className="ws-toast__dot"
            style={{ background: toast.tone === 'danger' ? 'var(--danger)' : 'var(--status-done)' }}
          />
          {toast.text}
        </div>
      )}
      {connOpen && (
        <ConnectionsDrawer data={connData} onClose={() => setConnOpen(false)} onRevoke={revokeConn} />
      )}
      {notifOpen && (
        <NotificationsPopover
          items={notifs}
          onClose={() => setNotifOpen(false)}
          onMarkAllRead={markAllRead}
          onDismiss={dismissNotif}
          onViewActivity={openActivity}
        />
      )}
      {activityOpen && (
        <ActivityDrawer
          data={activityData}
          filter={activityFilter}
          totalNotes={entries.length}
          search={activitySearch}
          onSearch={setActivitySearch}
          onFilter={changeActivityFilter}
          onRunCurator={runCurator}
          onClose={() => setActivityOpen(false)}
          showToast={showToast}
        />
      )}
      {folderToDelete !== null && (
        <div className="ws-overlay ws-overlay--center" onClick={() => setFolderToDelete(null)}>
          <div className="v-modal" style={{ width: 440 }} onClick={(e) => e.stopPropagation()}>
            <div className="v-modal__title">Delete this folder?</div>
            <div className="v-modal__body">
              The folder <span className="v-modal__code">{folderToDelete}</span> and its{' '}
              <b style={{ color: 'var(--danger)' }}>
                {entries.filter((e) => e.path.startsWith(folderToDelete + '/')).length}
              </b>{' '}
              note(s) will be removed from the vault. This can’t be undone.
            </div>
            <div className="v-modal__actions">
              <button className="v-modal__cancel" onClick={() => setFolderToDelete(null)}>
                Cancel
              </button>
              <button
                className="v-modal__confirm v-modal__confirm--danger"
                onClick={() => deleteFolder(folderToDelete)}
              >
                Delete folder
              </button>
            </div>
          </div>
        </div>
      )}
      {helpOpen && (
        <HelpModal
          endpoint={connData?.endpoint ?? `${window.location.origin}/mcp`}
          onClose={() => setHelpOpen(false)}
          onStartTour={startTour}
          showToast={showToast}
        />
      )}
      {tourStep !== null && (
        <TourOverlay
          step={tourStep}
          rootRef={rootRef}
          onPrev={() => setTourStep(Math.max(0, tourStep - 1))}
          onNext={() => (tourStep >= TOUR.length - 1 ? endTour() : setTourStep(tourStep + 1))}
          onSkip={endTour}
        />
      )}
    </div>
  )
}

// ---------------------------------------------------------------- top bar

function TopBar({
  version,
  activeTags,
  onRemoveTag,
  onOpenPalette,
  onToggleTheme,
  onOpenHelp,
  connActiveCount,
  onOpenConnections,
  starCount,
  unreadCount,
  onOpenNotifications,
  onOpenActivity,
  errorCount,
}: {
  version: string
  activeTags: string[]
  onRemoveTag: (t: string) => void
  onOpenPalette: () => void
  onToggleTheme: () => void
  onOpenHelp: () => void
  connActiveCount: number
  onOpenConnections: () => void
  starCount: number | null
  unreadCount: number
  onOpenNotifications: () => void
  onOpenActivity: () => void
  errorCount: number
}) {
  return (
    <header className="ws-topbar">
      <div className="ws-topbar__brand">
        <LogoMark size={27} variant="paper" surface="var(--bg)" />
        <span className="ws-topbar__wordmark">vellum</span>
      </div>
      <span className="ws-topbar__divider" />
      <button id="tourSearch" className="ws-topbar__search" onClick={onOpenPalette}>
        <Icon name="search" size={15} className="ws-topbar__search-icon" />
        <span className="ws-topbar__search-placeholder">Search notes…</span>
        <span className="ws-topbar__kbd">⌘K</span>
      </button>
      <span className="ws-topbar__tags">
        {activeTags.map((t) => (
          <span key={t} className="ws-active-tag">
            #{t}
            <span onClick={() => onRemoveTag(t)}>×</span>
          </span>
        ))}
      </span>
      <span className="ws-topbar__spacer" />
      <button className="ws-conn-pill" onClick={onOpenConnections} title="MCP connections">
        <span className="ws-conn-pill__dot" />
        <span className="ws-conn-pill__count">{connActiveCount}</span>
        <span className="ws-conn-pill__label">connected</span>
      </button>
      <a
        id="tourStar"
        className="ws-star"
        href="https://github.com/freema/vellum"
        target="_blank"
        rel="noopener"
        title="Star Vellum on GitHub"
      >
        <GithubMark size={14} />
        <span className="ws-star__label">Star</span>
        {starCount != null && (
          <span className="ws-star__count">
            <span className="ws-star__glyph">★</span>
            {starCount}
          </span>
        )}
      </a>
      <span className="ws-topbar__version">{version}</span>
      <button className="ws-icon-btn" onClick={onToggleTheme} title="Toggle midnight ink">
        <Icon name="moon" size={16} />
      </button>
      <button
        id="notifBtn"
        className="ws-icon-btn ws-notif-btn"
        onClick={onOpenNotifications}
        title="Notifications"
      >
        <Icon name="bell" size={16} />
        {unreadCount > 0 && <span className="ws-notif-btn__dot" />}
      </button>
      <button
        id="activityBtn"
        className="ws-icon-btn ws-activity-btn"
        onClick={onOpenActivity}
        title="Activity, errors & curator log"
      >
        <Icon name="activity" size={16} />
        {errorCount > 0 && <span className="ws-activity-btn__badge">{errorCount}</span>}
      </button>
      <button id="tourHelp" className="ws-icon-btn" onClick={onOpenHelp} title="Help & tour">
        <Icon name="help" size={16} />
      </button>
    </header>
  )
}

// ---------------------------------------------------------------- tree

function TreePanel({
  counts,
  matchCounts,
  filtering,
  projectDirs,
  otherTopDirs,
  selectedDir,
  onSelectDir,
  projectsOpen,
  onToggleProjects,
  tags,
  activeTags,
  onToggleTag,
  totalNotes,
  dropDir,
  dragging,
  onDropDir,
  onDragOverDir,
  addingFolder,
  selectedLabel,
  onStartAddFolder,
  onAddFolder,
  onCancelAddFolder,
  refreshing,
  onRefresh,
  onDeleteFolder,
}: {
  counts: Map<string, number>
  matchCounts: Map<string, number>
  filtering: boolean
  projectDirs: string[]
  otherTopDirs: string[]
  selectedDir: string
  onSelectDir: (dir: string) => void
  projectsOpen: boolean
  onToggleProjects: () => void
  tags: TagCount[]
  activeTags: string[]
  onToggleTag: (t: string) => void
  totalNotes: number
  dropDir: string | null
  dragging: boolean
  onDropDir: (dir: string) => void
  onDragOverDir: (dir: string | null) => void
  addingFolder: boolean
  selectedLabel: string
  onStartAddFolder: () => void
  onAddFolder: (name: string) => void
  onCancelAddFolder: () => void
  refreshing: boolean
  onRefresh: () => void
  onDeleteFolder: (dir: string) => void
}) {
  const dropProps = (dir: string) => ({
    onDragOver: (e: React.DragEvent) => {
      if (!dragging) return
      e.preventDefault()
      if (dropDir !== dir) onDragOverDir(dir)
    },
    onDragLeave: () => {
      if (dropDir === dir) onDragOverDir(null)
    },
    onDrop: (e: React.DragEvent) => {
      e.preventDefault()
      onDropDir(dir)
    },
  })

  // While a tag filter is active, every folder shows "matching / total"
  // (e.g. 1/6) with the matching part highlighted; otherwise the plain total.
  const renderCount = (dir: string, asBadge: boolean) => {
    const total = counts.get(dir) ?? 0
    if (filtering) {
      const m = matchCounts.get(dir) ?? 0
      return (
        <span className={`ws-tree__frac${m > 0 ? ' ws-tree__frac--hit' : ''}`}>
          <b>{m}</b>/{total}
        </span>
      )
    }
    if (asBadge && total > 0) return <span className="ws-tree__badge">{total}</span>
    return <span className="ws-tree__count">{total}</span>
  }

  const row = (
    dir: string,
    label: string,
    icon: IconName,
    opts: {
      badge?: boolean
      caret?: 'open' | 'closed'
      onCaret?: () => void
      deletable?: boolean
    } = {},
  ) => {
    const selected = selectedDir === dir
    const isDrop = dropDir === dir
    return (
      <div
        className={`ws-tree__row${selected ? ' ws-tree__row--selected' : ''}${isDrop ? ' ws-tree__row--drop' : ''}`}
        onClick={() => onSelectDir(selected ? '' : dir)}
        {...dropProps(dir)}
      >
        <span
          className="ws-tree__caret"
          onClick={
            opts.onCaret
              ? (e) => {
                  e.stopPropagation()
                  opts.onCaret?.()
                }
              : undefined
          }
        >
          {opts.caret ? (opts.caret === 'open' ? '▾' : '▸') : ''}
        </span>
        <span className="ws-tree__glyph">
          <Icon name={icon} size={14} />
        </span>
        <span className="ws-tree__name">{label}</span>
        {opts.deletable && (
          <span
            className="ws-tree__del"
            title="Delete folder"
            onClick={(e) => {
              e.stopPropagation()
              onDeleteFolder(dir)
            }}
          >
            ×
          </span>
        )}
        {renderCount(dir, !!opts.badge)}
      </div>
    )
  }

  return (
    <aside id="tourTree" className="ws-tree vscroll">
      <div className="ws-tree__head">
        <span className="ws-tree__label">Vault</span>
        <span className="ws-tree__head-right">
          <span className="ws-tree__total">{totalNotes} notes</span>
          <button
            className="ws-tree__add"
            onClick={onRefresh}
            title="Re-scan the vault (pick up changes made via MCP)"
            disabled={refreshing}
          >
            <Icon name="refresh" size={13} className={refreshing ? 'ws-spin' : undefined} />
          </button>
          <button
            id="tourNewFolder"
            className="ws-tree__add"
            onClick={onStartAddFolder}
            title="New folder"
          >
            <Icon name="plus" size={14} />
          </button>
        </span>
      </div>
      {addingFolder && (
        <div className="ws-tree__newfolder">
          <span className="ws-tree__newfolder-glyph">
            <Icon name="folder" size={13} />
          </span>
          <input
            className="ws-tree__newfolder-input"
            autoFocus
            placeholder={`New folder in ${selectedLabel}`}
            onKeyDown={(e) => {
              if (e.key === 'Enter') onAddFolder((e.target as HTMLInputElement).value)
              else if (e.key === 'Escape') {
                e.stopPropagation()
                onCancelAddFolder()
              }
            }}
            onBlur={(e) => onAddFolder(e.target.value)}
          />
        </div>
      )}
      {row('inbox', 'Inbox', 'inbox', { badge: true })}
      {row('projects', 'Projects', 'folder', {
        caret: projectsOpen ? 'open' : 'closed',
        onCaret: onToggleProjects,
      })}
      {projectsOpen &&
        projectDirs.map((dir) => {
          const active = selectedDir === dir
          const isDrop = dropDir === dir
          return (
            <div
              key={dir}
              className={`ws-tree__row ws-tree__child${active ? ' ws-tree__child--active' : ''}${isDrop ? ' ws-tree__row--drop' : ''}`}
              onClick={() => onSelectDir(active ? 'projects' : dir)}
              {...dropProps(dir)}
            >
              <span className="ws-tree__rail" />
              <span className="ws-tree__glyph" style={active ? { color: 'var(--accent)' } : undefined}>
                <Icon name="folder" size={13} />
              </span>
              <span className="ws-tree__name">{dir.split('/')[1]}</span>
              <span
                className="ws-tree__del"
                title="Delete folder"
                onClick={(e) => {
                  e.stopPropagation()
                  onDeleteFolder(dir)
                }}
              >
                ×
              </span>
              {renderCount(dir, false)}
            </div>
          )
        })}
      {otherTopDirs.map((dir) => row(dir, dir, 'folder', { deletable: true }))}
      {row('archive', 'Archive', 'archive')}
      <div className="ws-tree__divider" />
      <div className="ws-tree__label">Tags</div>
      <div className="ws-tree__cloud">
        {tags.map((t) => (
          <button
            key={t.tag}
            className={`ws-tag-pill${activeTags.includes(t.tag) ? ' ws-tag-pill--active' : ''}`}
            onClick={() => onToggleTag(t.tag)}
          >
            #{t.tag}
          </button>
        ))}
      </div>
    </aside>
  )
}

// ---------------------------------------------------------------- list

function ListPanel({
  entries,
  selectedDir,
  selectedPath,
  typeFilter,
  statusFilter,
  onTypeFilter,
  onStatusFilter,
  onOpen,
  onHover,
  onCreate,
  onDragStart,
  onDragEnd,
}: {
  entries: NoteEntry[]
  selectedDir: string
  selectedPath: string
  typeFilter: TypeFilter
  statusFilter: string
  onTypeFilter: (t: TypeFilter) => void
  onStatusFilter: (s: string) => void
  onOpen: (path: string) => void
  onHover: (path: string) => void
  onCreate: () => void
  onDragStart: (path: string) => void
  onDragEnd: () => void
}) {
  const crumb = selectedDir === '' ? 'vault' : selectedDir.split('/').join(' / ')
  return (
    <section id="tourList" className="ws-list">
      <div className="ws-list__header">
        <div className="ws-list__crumb-row">
          <span className="ws-list__crumb">{crumb}</span>
          <button className="ws-list__add" onClick={onCreate} title="New note">
            <Icon name="plus" size={16} />
          </button>
        </div>
        <div className="ws-type-toggle">
          {(
            [
              ['all', 'All', null],
              ['task', 'Tasks', <span key="d" className="ws-type-toggle__dot" />],
              ['knowledge', 'Knowledge', <span key="s" className="ws-type-toggle__square" />],
            ] as const
          ).map(([key, label, marker]) => (
            <button
              key={key}
              className={`ws-type-toggle__item${typeFilter === key ? ' ws-type-toggle__item--active' : ''}`}
              onClick={() => {
                if (key === 'knowledge') onStatusFilter('')
                onTypeFilter(key)
              }}
            >
              {marker}
              {label}
            </button>
          ))}
        </div>
        {typeFilter !== 'knowledge' && (
          <div className="ws-segmented">
            {(
              [
                ['', 'All'],
                ['backlog', 'Backlog'],
                ['in-progress', 'Doing'],
                ['done', 'Done'],
              ] as const
            ).map(([key, label]) => (
              <button
                key={key}
                className={`ws-segmented__item${statusFilter === key ? ' ws-segmented__item--active' : ''}`}
                onClick={() => onStatusFilter(key)}
              >
                {label}
              </button>
            ))}
          </div>
        )}
      </div>
      <div className="ws-list__body vscroll">
        {entries.map((e) => (
          <div
            key={e.path}
            className={`ws-note-row${e.path === selectedPath ? ' ws-note-row--active' : ''}`}
            onClick={() => onOpen(e.path)}
            onMouseEnter={() => onHover(e.path)}
            draggable
            onDragStart={(ev) => {
              ev.dataTransfer.effectAllowed = 'move'
              onDragStart(e.path)
            }}
            onDragEnd={onDragEnd}
          >
            <div className="ws-note-row__title-line">
              {noteType(e) === 'task' ? (
                <span
                  className="ws-note-row__dot"
                  style={{ background: STATUS[e.status ?? 'backlog']?.dot ?? 'var(--status-backlog)' }}
                />
              ) : (
                <span className="ws-note-row__square" />
              )}
              <span className="ws-note-row__title">{e.title}</span>
            </div>
            {e.excerpt && <div className="ws-note-row__snippet">{e.excerpt}</div>}
            <div className="ws-note-row__meta">
              {(e.tags ?? []).slice(0, 3).map((t) => (
                <span key={t} className="ws-note-row__tag">
                  #{t}
                </span>
              ))}
              <span style={{ flex: 1 }} />
              <span className="ws-note-row__age">{relativeAge(e.modTime)}</span>
            </div>
          </div>
        ))}
        {entries.length === 0 && (
          <div className="ws-list__empty">
            <div className="ws-list__empty-glyph">∅</div>
            <div className="ws-list__empty-title">Nothing here</div>
            <div className="ws-list__empty-body">No notes match this filter.</div>
          </div>
        )}
      </div>
    </section>
  )
}

// ---------------------------------------------------------------- editor

const TOOLBAR: { icon?: IconName; label?: string; title: string; kind: FormatKind }[] = [
  { label: 'B', title: 'Bold  ⌘B', kind: 'bold' },
  { label: 'I', title: 'Italic  ⌘I', kind: 'italic' },
  { label: 'H1', title: 'Heading 1', kind: 'h1' },
  { label: 'H2', title: 'Heading 2', kind: 'h2' },
  { icon: 'list', title: 'Bullet list', kind: 'list' },
  { icon: 'checkSquare', title: 'Checkbox', kind: 'check' },
  { icon: 'quote', title: 'Quote', kind: 'quote' },
  { icon: 'code', title: 'Inline code', kind: 'code' },
  { icon: 'link', title: 'Link a note', kind: 'wiki' },
]

/** Editor-pane error states per design/Vellum-Error-Pages.dc.html: a missing
 * deep link (404), a note deleted while open (410) and a failed read (500). */
function NoteErrorState({
  kind,
  path,
  onOpenPalette,
  onBack,
  onRetry,
}: {
  kind: 'notfound' | 'gone' | 'error'
  path: string
  onOpenPalette: () => void
  onBack: () => void
  onRetry: () => void
}) {
  const dir = path.includes('/') ? path.slice(0, path.lastIndexOf('/') + 1) : ''
  const searchBtn = (
    <button className="v-btn v-btn--secondary" onClick={onOpenPalette}>
      Search the vault ⌘K
    </button>
  )
  const backBtn = (primary: boolean) => (
    <button className={`v-btn ${primary ? 'v-btn--primary' : 'v-btn--secondary'}`} onClick={onBack}>
      Back to inbox
    </button>
  )

  if (kind === 'gone') {
    return (
      <div className="ws-err">
        <div className="ws-err__glyph">⌫</div>
        <div className="ws-err__label ws-err__label--danger">HTTP 410 · Gone</div>
        <div className="ws-err__title">This note was deleted</div>
        <div className="ws-err__body">
          It was removed from the vault — by an agent or another editor — and is no longer
          available.
        </div>
        <div className="ws-err__detail">
          <s>{path}</s>
        </div>
        <div className="ws-err__actions">
          {backBtn(true)}
          {searchBtn}
        </div>
      </div>
    )
  }

  if (kind === 'error') {
    return (
      <div className="ws-err">
        <div className="ws-err__code ws-err__code--danger">500</div>
        <div className="ws-err__label ws-err__label--danger">HTTP 500 · Server error</div>
        <div className="ws-err__title">The vault couldn&apos;t be read</div>
        <div className="ws-err__body">
          Something broke while reading the note. Your files are safe — this is on the server. Try
          again in a moment.
        </div>
        <div className="ws-err__actions">
          <button className="v-btn v-btn--primary" onClick={onRetry}>
            Retry
          </button>
          {backBtn(false)}
        </div>
      </div>
    )
  }

  return (
    <div className="ws-err">
      <div className="ws-err__code">404</div>
      <div className="ws-err__label">HTTP 404 · Not found</div>
      <div className="ws-err__title">No note lives here</div>
      <div className="ws-err__body">
        The path didn&apos;t resolve to anything in the vault. It may have been renamed or moved to
        another folder.
      </div>
      <div className="ws-err__detail">
        {dir}
        <b>{basename(path)}</b>
      </div>
      <div className="ws-err__actions">
        <button className="v-btn v-btn--primary" onClick={onOpenPalette}>
          Search the vault ⌘K
        </button>
        {backBtn(false)}
      </div>
    </div>
  )
}

function EditorPanel({
  api,
  path,
  allDirs,
  onSaved,
  onSavingChange,
  onMove,
  onRename,
  onDelete,
  onOpenPalette,
  onBack,
  showToast,
}: {
  api: ApiClient
  path: string
  allDirs: string[]
  onSaved: () => void
  onSavingChange: (saving: boolean) => void
  onMove: (from: string, dir: string) => void
  onRename: (from: string, name: string) => void
  onDelete: (path: string) => void
  onOpenPalette: () => void
  onBack: () => void
  showToast: (text: string, tone?: 'ok' | 'danger') => void
}) {
  const [note, setNote] = useState<Note | null>(null)
  const [loading, setLoading] = useState(false)
  const [loadError, setLoadError] = useState<'notfound' | 'gone' | 'error' | null>(null)
  const [retryTick, setRetryTick] = useState(0)
  const [draft, setDraft] = useState('')
  const [titleDraft, setTitleDraft] = useState('')
  const [etag, setEtag] = useState('')
  const [mode, setMode] = useState<ViewMode>('split')
  const [conflict, setConflict] = useState<{ content: string; etag: string } | null>(null)
  const [statusMenu, setStatusMenu] = useState(false)
  const [moveMenu, setMoveMenu] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const saveTimer = useRef<number | undefined>(undefined)
  const rawRef = useRef<HTMLTextAreaElement>(null)
  // Latest note/draft/etag for the revalidation poll — reading state through a
  // ref keeps the poll callback stable and out of the effect deps.
  const liveRef = useRef({ note, draft, etag })
  useEffect(() => {
    liveRef.current = { note, draft, etag }
  })

  useEffect(() => {
    if (!path) return
    let cancelled = false
    setLoading(true)
    setLoadError(null)
    void api
      .getNote(path)
      .then((n) => {
        if (cancelled) return
        setNote(n)
        setDraft(n.content)
        setTitleDraft(n.title)
        setEtag(n.hash)
        onSavingChange(false)
      })
      .catch((err) => {
        if (cancelled || err instanceof AuthError) return
        setLoadError(err instanceof NotFoundError ? 'notfound' : 'error')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [api, path, onSavingChange, retryTick])

  // Keep the open note in sync with the vault: an MCP write shows up without a
  // reload, an external delete switches to the "deleted" state. Local edits are
  // never touched — a remote change only lands when the draft is clean (a dirty
  // draft keeps the existing If-Match → conflict flow).
  useEffect(() => {
    if (!path) return
    let alive = true
    const revalidate = async () => {
      const { note: cur } = liveRef.current
      if (!cur || document.hidden) return
      try {
        const n = await api.getNote(path) // cached + If-None-Match → cheap 304
        if (!alive) return
        setLoadError((e) => (e === 'gone' ? null : e)) // recreated meanwhile
        const { note: still, draft: d } = liveRef.current
        if (!still || n.hash === still.hash || d !== still.content) return
        setNote(n)
        setDraft(n.content)
        setTitleDraft(n.title)
        setEtag(n.hash)
      } catch (err) {
        if (alive && err instanceof NotFoundError) setLoadError('gone')
      }
    }
    const id = window.setInterval(() => void revalidate(), 20000)
    const onFocus = () => void revalidate()
    window.addEventListener('focus', onFocus)
    return () => {
      alive = false
      window.clearInterval(id)
      window.removeEventListener('focus', onFocus)
    }
  }, [api, path])

  const save = useCallback(
    async (content: string, ifMatch: string) => {
      try {
        const newTag = await api.putNote(path, content, ifMatch)
        setEtag(newTag)
        // Mirror the saved state into the note object — the revalidation poll
        // compares against it to tell "clean draft" from "unsaved edits".
        setNote((cur) => (cur ? { ...cur, content, hash: newTag } : cur))
        onSavingChange(false)
        onSaved()
      } catch (err) {
        if (err instanceof ConflictError) {
          setConflict({ content: err.content, etag: err.etag })
        } else if (!(err instanceof AuthError)) {
          console.error(err)
          onSavingChange(false)
        }
      }
    },
    [api, path, onSaved, onSavingChange],
  )

  const queueSave = useCallback(
    (content: string) => {
      onSavingChange(true)
      window.clearTimeout(saveTimer.current)
      saveTimer.current = window.setTimeout(() => void save(content, etag), 1000)
    },
    [save, etag, onSavingChange],
  )

  const onEdit = (value: string) => {
    setDraft(value)
    queueSave(value)
  }

  // apply immediately (checkbox toggle, title, status) without debounce
  const commit = useCallback(
    (content: string) => {
      setDraft(content)
      onSavingChange(true)
      window.clearTimeout(saveTimer.current)
      void save(content, etag)
    },
    [save, etag, onSavingChange],
  )

  const applyFormat = (kind: FormatKind) => {
    const el = rawRef.current
    if (!el) return
    const next = formatSelection(el, kind)
    if (next) {
      setDraft(next.value)
      queueSave(next.value)
      requestAnimationFrame(() => {
        el.focus()
        try {
          el.setSelectionRange(next.start, next.end)
        } catch {
          /* ignore */
        }
      })
    }
  }

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const inRaw = document.activeElement === rawRef.current
      if ((e.metaKey || e.ctrlKey) && (e.key === 's' || e.key === 'S')) {
        e.preventDefault()
        window.clearTimeout(saveTimer.current)
        void save(draft, etag)
      }
      if (inRaw && (e.metaKey || e.ctrlKey) && (e.key === 'b' || e.key === 'B')) {
        e.preventDefault()
        applyFormat('bold')
      }
      if (inRaw && (e.metaKey || e.ctrlKey) && (e.key === 'i' || e.key === 'I')) {
        e.preventDefault()
        applyFormat('italic')
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft, etag, save])

  if (path && loadError) {
    return (
      <main className="ws-editor">
        <NoteErrorState
          kind={loadError}
          path={path}
          onOpenPalette={onOpenPalette}
          onBack={onBack}
          onRetry={() => setRetryTick((t) => t + 1)}
        />
      </main>
    )
  }

  if (!path || !note) {
    return (
      <main className="ws-editor">
        <div className="ws-editor__empty">
          {path && loading ? (
            <div className="ws-loading">
              <span className="ws-spinner ws-spinner--lg" />
              <div className="ws-loading__text">Opening note…</div>
            </div>
          ) : (
            <div className="v-empty" style={{ border: 'none', background: 'transparent' }}>
              <div className="v-empty__glyph">∅</div>
              <div className="v-empty__title">No note open</div>
              <div className="v-empty__body">Pick a note from the list or press ⌘K.</div>
            </div>
          )}
        </div>
      </main>
    )
  }

  // Derive type/status from the live draft so the header updates immediately
  // after a status change or an inline frontmatter edit (the loaded note object
  // is only re-fetched when the path changes).
  const type = frontmatterValue(draft, 'type') === 'task' ? 'task' : 'knowledge'
  const status = frontmatterValue(draft, 'status')
  const st = STATUS[status] ?? STATUS.backlog
  const draftBody = stripFrontmatter(draft)
  const wordCount = draftBody.trim() ? draftBody.trim().split(/\s+/).length : 0
  const showRaw = mode !== 'preview'
  const showPreview = mode !== 'edit'

  const commitTitle = () => {
    const t = titleDraft.trim() || 'Untitled'
    if (t === note.title) return
    commit(setTitle(draft, t))
  }
  const setStatus = (next: string) => {
    setStatusMenu(false)
    if (next === status) return
    commit(setFrontmatterStatus(draft, next))
    showToast(`Marked ${STATUS[next].label.toLowerCase()}`)
  }
  const toggleCheckbox = (index: number) => {
    const next = toggleTaskLine(draft, index)
    if (next !== draft) commit(next)
  }

  return (
    <main className="ws-editor">
      {loading && <div className="ws-editor__loadbar" />}
      <div className="ws-editor__header">
        <div className="ws-editor__title-row">
          <input
            className="ws-editor__title-input"
            value={titleDraft}
            onChange={(e) => setTitleDraft(e.target.value)}
            onBlur={commitTitle}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                ;(e.target as HTMLInputElement).blur()
              }
            }}
            spellCheck={false}
          />
          <div className="ws-editor__actions">
            <div id="tourModes" className="ws-segmented ws-editor__modes">
              {(['edit', 'split', 'preview'] as const).map((m) => (
                <button
                  key={m}
                  className={`ws-segmented__item${mode === m ? ' ws-segmented__item--active' : ''}`}
                  onClick={() => setMode(m)}
                >
                  {m[0].toUpperCase() + m.slice(1)}
                </button>
              ))}
            </div>
            <button className="ws-icon-btn ws-icon-btn--danger" title="Delete note" onClick={() => setConfirmDelete(true)}>
              <Icon name="trash" size={15} />
            </button>
          </div>
        </div>
        <div className="ws-editor__meta">
          <span className="ws-type-badge">
            {type === 'task' ? (
              <span className="ws-note-row__dot" style={{ background: st.dot }} />
            ) : (
              <span className="ws-note-row__square" style={{ width: 7, height: 7 }} />
            )}
            {type}
          </span>
          {type === 'task' && (
            <span className="ws-status-chip-wrap">
              <button
                className="ws-status-chip"
                style={{ color: st.text, background: st.soft }}
                onClick={() => {
                  setStatusMenu((o) => !o)
                  setMoveMenu(false)
                }}
              >
                <span className="ws-note-row__dot" style={{ background: st.dot }} />
                {st.label}
                <span className="ws-status-chip__caret">▾</span>
              </button>
              {statusMenu && (
                <div className="ws-menu ws-status-menu">
                  {STATUS_ORDER.map((k) => (
                    <div
                      key={k}
                      className={`ws-menu__item${status === k ? ' ws-menu__item--current' : ''}`}
                      onClick={() => setStatus(k)}
                    >
                      <span className="ws-note-row__dot" style={{ background: STATUS[k].dot }} />
                      {STATUS[k].label}
                    </div>
                  ))}
                </div>
              )}
            </span>
          )}
          {(note.tags ?? []).map((t) => (
            <span key={t} className="ws-meta-tag">
              #{t}
            </span>
          ))}
          <span style={{ flex: 1 }} />
          <span className="ws-move-wrap">
            <button
              className="ws-move-btn"
              onClick={() => {
                setMoveMenu((o) => !o)
                setStatusMenu(false)
              }}
            >
              <Icon name="move" size={14} />
              Move to…
            </button>
            {moveMenu && (
              <MovePopover
                path={path}
                allDirs={allDirs}
                onMove={(dir) => {
                  setMoveMenu(false)
                  onMove(path, dir)
                }}
                onRename={(name) => {
                  setMoveMenu(false)
                  onRename(path, name)
                }}
                onClose={() => setMoveMenu(false)}
              />
            )}
          </span>
          <span className="ws-editor__modified">modified {relativeAge(Date.parse(note.modTime) / 1000)}</span>
        </div>
      </div>

      {showRaw && (
        <div id="tourToolbar" className="ws-toolbar">
          {TOOLBAR.map((b, i) => (
            <button
              key={i}
              className={`ws-toolbar__btn${b.label === 'B' ? ' ws-toolbar__btn--bold' : ''}${b.label === 'I' ? ' ws-toolbar__btn--italic' : ''}`}
              title={b.title}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => applyFormat(b.kind)}
            >
              {b.icon ? <Icon name={b.icon} size={15} /> : b.label}
            </button>
          ))}
          <span style={{ flex: 1 }} />
          <span className="ws-toolbar__count">
            {wordCount} {wordCount === 1 ? 'word' : 'words'}
          </span>
        </div>
      )}

      <div className="ws-editor__body">
        {showRaw && (
          <textarea
            ref={rawRef}
            className={`ws-editor__raw vscroll${showPreview ? '' : ' ws-editor__raw--full'}`}
            value={draft}
            onChange={(e) => onEdit(e.target.value)}
            spellCheck={false}
          />
        )}
        {showPreview && (
          <div className="ws-editor__preview vscroll">
            <MarkdownView body={draftBody} onToggleCheckbox={toggleCheckbox} />
          </div>
        )}
      </div>

      {confirmDelete && (
        <div className="ws-overlay ws-overlay--center" onClick={() => setConfirmDelete(false)}>
          <div className="v-modal" style={{ width: 420 }} onClick={(e) => e.stopPropagation()}>
            <div className="v-modal__title">Delete this note?</div>
            <div className="v-modal__body">
              <span className="v-modal__code">{basename(path)}</span> will be removed from the vault.
              This can’t be undone.
            </div>
            <div className="v-modal__actions">
              <button className="v-modal__cancel" onClick={() => setConfirmDelete(false)}>
                Cancel
              </button>
              <button
                className="v-modal__confirm v-modal__confirm--danger"
                onClick={() => {
                  setConfirmDelete(false)
                  onDelete(path)
                }}
              >
                Delete
              </button>
            </div>
          </div>
        </div>
      )}

      {conflict && (
        <div className="ws-overlay ws-overlay--center">
          <div className="v-modal" style={{ width: 440 }}>
            <div className="v-modal__title">Note changed on disk</div>
            <div className="v-modal__body">
              Someone else saved <span className="v-modal__code">{path}</span> while you were editing.
              Load the latest version, or overwrite it with yours.
            </div>
            <div className="v-modal__actions">
              <button
                className="v-modal__cancel"
                onClick={() => {
                  setDraft(conflict.content)
                  setEtag(conflict.etag)
                  onSavingChange(false)
                  setConflict(null)
                }}
              >
                Load latest version
              </button>
              <button
                className="v-modal__confirm"
                style={{ background: 'var(--accent)', borderColor: 'var(--accent)' }}
                onClick={() => {
                  void save(draft, conflict.etag)
                  setConflict(null)
                }}
              >
                Overwrite with mine
              </button>
            </div>
          </div>
        </div>
      )}
    </main>
  )
}

// ---------------------------------------------------------------- move popover

function MovePopover({
  path,
  allDirs,
  onMove,
  onRename,
  onClose,
}: {
  path: string
  allDirs: string[]
  onMove: (dir: string) => void
  onRename: (name: string) => void
  onClose: () => void
}) {
  const [name, setName] = useState(basename(path))
  const currentDir = dirname(path)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const onDoc = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose()
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [onClose])
  return (
    <div className="ws-menu ws-move-menu" ref={ref}>
      <div className="ws-menu__label">Rename file</div>
      <input
        className="ws-move-menu__input"
        value={name}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault()
            if (name.trim() && name !== basename(path)) onRename(name)
          }
        }}
        spellCheck={false}
      />
      <div className="ws-menu__label">Move to folder</div>
      <div className="ws-move-menu__list vscroll">
        {allDirs.map((dir) => (
          <div
            key={dir}
            className={`ws-menu__item${dir === currentDir ? ' ws-menu__item--current' : ''}`}
            onClick={() => onMove(dir)}
          >
            <Icon name="folder" size={13} />
            {dir.split('/').join(' / ')}
            <span style={{ flex: 1 }} />
            {dir === currentDir && <span className="ws-menu__check">✓</span>}
          </div>
        ))}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------- markdown

function stripFrontmatter(content: string): string {
  if (!content.startsWith('---\n')) return content
  const end = content.indexOf('\n---\n', 4)
  if (end < 0) return content
  return content.slice(end + 5)
}

/** Read a top-level frontmatter scalar (e.g. type, status) from live content. */
function frontmatterValue(content: string, key: string): string {
  if (!content.startsWith('---\n')) return ''
  const end = content.indexOf('\n---\n', 4)
  if (end < 0) return ''
  const line = content
    .slice(4, end)
    .split('\n')
    .find((l) => new RegExp(`^${key}:`, 'i').test(l))
  return line ? line.slice(line.indexOf(':') + 1).trim() : ''
}

function MarkdownView({
  body,
  onToggleCheckbox,
}: {
  body: string
  onToggleCheckbox?: (index: number) => void
}) {
  const navigate = useNavigate()
  const processed = body.replace(
    /\[\[([^\][|]+)(?:\|([^\][]+))?\]\]/g,
    (_, target: string, alias?: string) =>
      `[${alias ?? target}](#wikilink=${encodeURIComponent(target.trim())})`,
  )
  const cb = { i: 0 }
  return (
    <div className="v-markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          a({ href, children }) {
            if (href?.startsWith('#wikilink=')) {
              const target = decodeURIComponent(href.slice('#wikilink='.length))
              return (
                <a
                  className="v-wikilink"
                  onClick={(e) => {
                    e.preventDefault()
                    navigate(`/wl/${encodeURIComponent(target)}`)
                  }}
                >
                  [[{children}]]
                </a>
              )
            }
            return (
              <a href={href} target="_blank" rel="noreferrer">
                {children}
              </a>
            )
          },
          input({ checked }) {
            const idx = cb.i++
            return (
              <span
                className={`v-checkbox${checked ? ' v-checkbox--checked' : ''}${onToggleCheckbox ? ' v-checkbox--live' : ''}`}
                onClick={onToggleCheckbox ? () => onToggleCheckbox(idx) : undefined}
              >
                {checked ? '✓' : ''}
              </span>
            )
          },
        }}
      >
        {processed}
      </ReactMarkdown>
    </div>
  )
}

// ---------------------------------------------------------------- content edits

/** Replace the note title, following deriveTitle precedence (fm title → first heading → prepend). */
function setTitle(content: string, title: string): string {
  if (content.startsWith('---\n')) {
    const end = content.indexOf('\n---\n', 4)
    if (end >= 0) {
      const front = content.slice(4, end)
      const rest = content.slice(end + 5)
      const lines = front.split('\n')
      const ti = lines.findIndex((l) => /^title:/i.test(l))
      if (ti >= 0) lines[ti] = `title: ${title}`
      else lines.unshift(`title: ${title}`)
      return `---\n${lines.join('\n')}\n---\n${rest}`
    }
  }
  // no frontmatter — edit first H1 or prepend one
  const lines = content.split('\n')
  const hi = lines.findIndex((l) => /^#\s+/.test(l))
  if (hi >= 0) {
    lines[hi] = `# ${title}`
    return lines.join('\n')
  }
  return `# ${title}\n\n${content}`
}

/** Set the frontmatter `status:` line, creating frontmatter if absent. */
function setFrontmatterStatus(content: string, status: string): string {
  if (content.startsWith('---\n')) {
    const end = content.indexOf('\n---\n', 4)
    if (end >= 0) {
      const lines = content.slice(4, end).split('\n')
      const si = lines.findIndex((l) => /^status:/i.test(l))
      if (si >= 0) lines[si] = `status: ${status}`
      else lines.push(`status: ${status}`)
      return `---\n${lines.join('\n')}\n---\n${content.slice(end + 5)}`
    }
  }
  return `---\ntype: task\nstatus: ${status}\n---\n\n${content}`
}

/** Flip the index-th GFM task checkbox in the body region. */
function toggleTaskLine(content: string, index: number): string {
  const body = stripFrontmatter(content)
  const offset = content.length - body.length
  const lines = body.split('\n')
  let n = -1
  for (let i = 0; i < lines.length; i++) {
    const m = lines[i].match(/^(\s*[-*+] )\[([ xX])\]/)
    if (!m) continue
    n++
    if (n === index) {
      const done = m[2].toLowerCase() === 'x'
      lines[i] = lines[i].replace(/^(\s*[-*+] )\[[ xX]\]/, `$1[${done ? ' ' : 'x'}]`)
      return content.slice(0, offset) + lines.join('\n')
    }
  }
  return content
}

type FormatKind = 'bold' | 'italic' | 'code' | 'wiki' | 'h1' | 'h2' | 'list' | 'check' | 'quote'

/** Apply a markdown format to the textarea's current selection. */
function formatSelection(
  el: HTMLTextAreaElement,
  kind: FormatKind,
): { value: string; start: number; end: number } | null {
  const val = el.value
  const s = el.selectionStart
  const e = el.selectionEnd
  const sel = val.slice(s, e)
  const pair = (open: string, close: string, ph: string) => {
    const inner = sel || ph
    const value = val.slice(0, s) + open + inner + close + val.slice(e)
    const ns = s + open.length
    return { value, start: ns, end: ns + inner.length }
  }
  const line = (prefix: string) => {
    let ls = val.lastIndexOf('\n', s - 1) + 1
    let le = val.indexOf('\n', e)
    if (le === -1) le = val.length
    const block = val.slice(ls, le)
    const nb = block
      .split('\n')
      .map((l) => prefix + l)
      .join('\n')
    return { value: val.slice(0, ls) + nb + val.slice(le), start: ls, end: ls + nb.length }
  }
  switch (kind) {
    case 'bold':
      return pair('**', '**', 'bold')
    case 'italic':
      return pair('*', '*', 'italic')
    case 'code':
      return pair('`', '`', 'code')
    case 'wiki':
      return pair('[[', ']]', 'note')
    case 'h1':
      return line('# ')
    case 'h2':
      return line('## ')
    case 'list':
      return line('- ')
    case 'check':
      return line('- [ ] ')
    case 'quote':
      return line('> ')
    default:
      return null
  }
}

// ---------------------------------------------------------------- help modal

function HelpModal({
  endpoint,
  onClose,
  onStartTour,
  showToast,
}: {
  endpoint: string
  onClose: () => void
  onStartTour: () => void
  showToast: (text: string, tone?: 'ok' | 'danger') => void
}) {
  const cliCmd = `claude mcp add --transport http vellum ${endpoint}`
  const copy = (text: string, label: string) => {
    void navigator.clipboard?.writeText(text).then(
      () => showToast(`${label} copied`),
      () => showToast('Copy failed', 'danger'),
    )
  }
  const md: [string, string][] = [
    ['# H1', 'Heading'],
    ['**b**', 'Bold'],
    ['*i*', 'Italic'],
    ['- item', 'List'],
    ['- [ ]', 'Checkbox'],
    ['> q', 'Quote'],
    ['`code`', 'Inline code'],
    ['[[note]]', 'Link a note'],
  ]
  const keys: [string, string][] = [
    ['⌘K', 'Search notes'],
    ['⌘B', 'Bold selection'],
    ['⌘I', 'Italic selection'],
    ['⌘S', 'Save now'],
    ['esc', 'Close panel'],
  ]
  return (
    <div className="ws-overlay ws-overlay--center" onClick={onClose}>
      <div className="ws-help vscroll" onClick={(e) => e.stopPropagation()}>
        <div className="ws-help__head">
          <div>
            <div className="ws-help__title">Help</div>
            <div className="ws-help__sub">Markdown syntax and keyboard shortcuts.</div>
          </div>
          <button className="ws-help__esc" onClick={onClose}>
            esc
          </button>
        </div>
        <div className="ws-help__body">
          <button className="ws-help__tour" onClick={onStartTour}>
            Take the guided tour →
          </button>
          <div className="ws-help__grid">
            <div>
              <div className="ws-help__label">Markdown</div>
              <div className="ws-help__rows">
                {md.map(([code, desc]) => (
                  <div key={code} className="ws-help__row">
                    <code className="ws-help__code">{code}</code>
                    <span className="ws-help__desc">{desc}</span>
                  </div>
                ))}
              </div>
            </div>
            <div>
              <div className="ws-help__label">Shortcuts</div>
              <div className="ws-help__rows">
                {keys.map(([k, desc]) => (
                  <div key={k} className="ws-help__row">
                    <span className="ws-help__kbd">{k}</span>
                    <span className="ws-help__desc">{desc}</span>
                  </div>
                ))}
              </div>
              <div className="ws-help__label" style={{ marginTop: 24 }}>
                Note types
              </div>
              <div className="ws-help__rows">
                <div className="ws-help__row">
                  <span className="ws-note-row__dot" style={{ background: 'var(--status-inprogress)' }} />
                  <span className="ws-help__desc">Task — carries a status</span>
                </div>
                <div className="ws-help__row">
                  <span className="ws-note-row__square" />
                  <span className="ws-help__desc">Knowledge — reference</span>
                </div>
              </div>
            </div>
          </div>
          <div className="ws-help__divider" />
          <div className="ws-help__label">Connect a client</div>
          <div className="ws-help__endpoint">
            <span className="ws-help__endpoint-label">endpoint</span>
            <span className="ws-help__endpoint-url">{endpoint}</span>
            <button className="ws-help__copy" onClick={() => copy(endpoint, 'Endpoint')}>
              Copy
            </button>
          </div>
          <div className="ws-help__connect-line">
            <strong>Claude.ai (web):</strong> Settings → Connectors → “Add custom connector” → paste
            the endpoint → authorize.
          </div>
          <div className="ws-help__cli">
            <code className="ws-help__cli-code">{cliCmd}</code>
            <button className="ws-help__copy" onClick={() => copy(cliCmd, 'Command')}>
              Copy
            </button>
          </div>
          <div className="ws-help__connect-note">
            Claude Code, Desktop, ChatGPT and Cursor setups live on the connect screen.
          </div>
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------- onboarding tour

function TourOverlay({
  step,
  rootRef,
  onPrev,
  onNext,
  onSkip,
}: {
  step: number
  rootRef: React.RefObject<HTMLDivElement | null>
  onPrev: () => void
  onNext: () => void
  onSkip: () => void
}) {
  const [rect, setRect] = useState<{
    top: number
    left: number
    width: number
    height: number
    cw: number
    ch: number
  } | null>(null)

  useEffect(() => {
    const measure = () => {
      const s = TOUR[step]
      const root = rootRef.current
      const el = document.getElementById(s.id)
      if (!root || !el) {
        setRect(null)
        return
      }
      const cr = root.getBoundingClientRect()
      const r = el.getBoundingClientRect()
      setRect({
        top: r.top - cr.top,
        left: r.left - cr.left,
        width: r.width,
        height: r.height,
        cw: cr.width,
        ch: cr.height,
      })
    }
    const id = requestAnimationFrame(measure)
    window.addEventListener('resize', measure)
    return () => {
      cancelAnimationFrame(id)
      window.removeEventListener('resize', measure)
    }
  }, [step, rootRef])

  // arrow keys walk the tour: → next / done, ← back, Esc skips
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
        e.preventDefault()
        onNext()
      } else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
        e.preventDefault()
        onPrev()
      } else if (e.key === 'Escape') {
        e.preventDefault()
        onSkip()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onNext, onPrev, onSkip])

  const s = TOUR[step]
  const W = 286
  let spotlight: React.CSSProperties = { display: 'none' }
  let tip: React.CSSProperties
  if (rect) {
    const pad = 6
    spotlight = {
      top: rect.top - pad,
      left: rect.left - pad,
      width: rect.width + pad * 2,
      height: rect.height + pad * 2,
    }
    let t: number
    let l: number
    if (s.place === 'right') {
      l = rect.left + rect.width + 16
      t = rect.top
    } else if (s.place === 'below-right') {
      t = rect.top + rect.height + 14
      l = rect.left + rect.width - W
    } else {
      t = rect.top + rect.height + 14
      l = rect.left
    }
    l = Math.max(14, Math.min(l, rect.cw - W - 14))
    t = Math.max(14, Math.min(t, rect.ch - 200))
    tip = { top: t, left: l, width: W }
  } else {
    tip = { top: '50%', left: '50%', transform: 'translate(-50%,-50%)', width: W }
  }

  return (
    <div className="ws-tour">
      <div className="ws-tour__spotlight" style={spotlight} />
      <div className="ws-tour__tip" style={tip}>
        <div className="ws-tour__index">
          <span>
            {step + 1} / {TOUR.length}
          </span>
          <span className="ws-tour__keys">← → keys</span>
        </div>
        <div className="ws-tour__title">{s.title}</div>
        <div className="ws-tour__body">{s.body}</div>
        <div className="ws-tour__actions">
          <span className="ws-tour__skip" onClick={onSkip}>
            Skip
          </span>
          <span style={{ flex: 1 }} />
          {step > 0 && (
            <button className="ws-tour__back" onClick={onPrev}>
              Back
            </button>
          )}
          <button className="ws-tour__next" onClick={onNext}>
            {step === TOUR.length - 1 ? 'Done' : 'Next'}
          </button>
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------- palette

function PaletteOverlay({
  api,
  onClose,
  onOpen,
}: {
  api: ApiClient
  onClose: () => void
  onOpen: (path: string) => void
}) {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<SearchResult[]>([])
  const [selected, setSelected] = useState(0)
  const [searching, setSearching] = useState(false)
  // Monotonic sequence — a slow older response must never overwrite a newer one.
  const seqRef = useRef(0)

  useEffect(() => {
    if (!query.trim()) {
      seqRef.current++
      setResults([])
      setSearching(false)
      return
    }
    setSearching(true)
    const t = window.setTimeout(() => {
      const seq = ++seqRef.current
      const q = query.trim()
      const tags = q
        .split(/\s+/)
        .filter((w) => w.startsWith('#') && w.length > 1)
        .map((w) => w.slice(1))
      const text = q
        .split(/\s+/)
        .filter((w) => !w.startsWith('#'))
        .join(' ')
      void api
        .search(text, tags, 8)
        .then((r) => {
          if (seq !== seqRef.current) return
          setResults(r.slice(0, 8))
          setSelected(0)
        })
        .finally(() => {
          if (seq === seqRef.current) setSearching(false)
        })
    }, 180)
    return () => window.clearTimeout(t)
  }, [api, query])

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setSelected((s) => Math.min(s + 1, results.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSelected((s) => Math.max(s - 1, 0))
    } else if (e.key === 'Enter' && results[selected]) {
      onOpen(results[selected].path)
    }
  }

  return (
    <div className="ws-overlay" onClick={onClose}>
      <div onClick={(e) => e.stopPropagation()} onKeyDown={onKey}>
        <CommandPalette query={query} onQueryChange={setQuery}>
          {searching && results.length === 0 && (
            <div className="ws-palette__searching">
              <span className="ws-spinner" />
              Searching…
            </div>
          )}
          {results.map((r, i) => (
            <PaletteItem
              key={r.path}
              title={r.title}
              path={r.path.includes('/') ? r.path.slice(0, r.path.lastIndexOf('/')) : ''}
              selected={i === selected}
              onClick={() => onOpen(r.path)}
              snippet={r.snippets?.[0] ? snippetWithHighlight(r.snippets[0]) : undefined}
            />
          ))}
        </CommandPalette>
      </div>
    </div>
  )
}

function snippetWithHighlight(s: { match: string; context: string }) {
  const line =
    s.context.split('\n').find((l) => l.toLowerCase().includes(s.match.toLowerCase())) ??
    s.context.split('\n')[0]
  const idx = line.toLowerCase().indexOf(s.match.toLowerCase())
  if (idx < 0) return <>{line}</>
  return (
    <>
      {line.slice(0, idx)}
      <Highlight>{line.slice(idx, idx + s.match.length)}</Highlight>
      {line.slice(idx + s.match.length)}
    </>
  )
}

// ---------------------------------------------------------------- MCP connections

function ConnectionsDrawer({
  data,
  onClose,
  onRevoke,
}: {
  data: ConnectionsData | null
  onClose: () => void
  onRevoke: (id: string) => void
}) {
  const conns = data?.connections ?? []
  return (
    <div className="ws-drawer-scrim" onClick={onClose}>
      <div className="ws-drawer vscroll" onClick={(e) => e.stopPropagation()}>
        <div className="ws-drawer__head">
          <div className="ws-drawer__head-row">
            <div>
              <div className="ws-drawer__title">MCP connections</div>
              <div className="ws-drawer__sub">Live sessions held in the server’s memory.</div>
            </div>
            <button className="ws-drawer__esc" onClick={onClose}>
              esc
            </button>
          </div>
          <div className="ws-conn__endpoint">
            <span className="ws-conn__endpoint-label">endpoint</span>
            <span className="ws-conn__endpoint-url">{data?.endpoint ?? ''}</span>
          </div>
          <div className="ws-conn__stats">
            <div className="ws-conn__stat">
              <div className="ws-conn__stat-n">{data?.activeCount ?? 0}</div>
              <div className="ws-conn__stat-l">active clients</div>
            </div>
            <div className="ws-conn__stat-div" />
            <div className="ws-conn__stat">
              <div className="ws-conn__stat-n">{data?.totalCalls ?? 0}</div>
              <div className="ws-conn__stat-l">tool calls today</div>
            </div>
          </div>
        </div>
        <div className="ws-drawer__body">
          {conns.map((c) => (
            <ConnectionCard key={c.id} c={c} onRevoke={onRevoke} />
          ))}
          {conns.length === 0 && (
            <div className="ws-drawer__empty">
              <div className="ws-drawer__empty-glyph">∅</div>
              <div className="ws-drawer__empty-title">No active sessions</div>
              <div className="ws-drawer__empty-sub">Nothing is connected to the vault right now.</div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function ConnectionCard({ c, onRevoke }: { c: Connection; onRevoke: (id: string) => void }) {
  const live = c.status === 'active'
  return (
    <div className="ws-conn-card">
      <div className="ws-conn-card__top">
        <div className={`ws-conn-card__mono${live ? '' : ' ws-conn-card__mono--idle'}`}>{c.mono}</div>
        <div className="ws-conn-card__id">
          <div className="ws-conn-card__name-row">
            <span className="ws-conn-card__name">{c.name}</span>
            <span className={`ws-conn-card__status ws-conn-card__status--${c.status}`}>
              <span className="ws-conn-card__status-dot" />
              {live ? 'live' : 'idle'}
            </span>
          </div>
          <div className="ws-conn-card__kind">{c.kind}</div>
        </div>
      </div>
      <div className="ws-conn-card__divider" />
      <div className="ws-conn-card__meta">
        <span className="ws-conn-card__sid">{c.id}</span>
        <span className="ws-conn-card__mid-dot">·</span>
        <span>up {c.since}</span>
      </div>
      <div className="ws-conn-card__foot">
        {c.lastTool && <span className="ws-conn-card__tool">{c.lastTool}</span>}
        <span className="ws-conn-card__ago">{c.lastAgo} ago</span>
        <span className="ws-conn-card__spacer" />
        <span className="ws-conn-card__calls">{c.calls} calls</span>
        <span className="ws-conn-card__revoke" onClick={() => onRevoke(c.id)}>
          Revoke
        </span>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------- notifications

const NOTIF_DOT: Record<string, string> = {
  curator: 'var(--accent)',
  task: 'var(--status-inprogress)',
  mcp: 'var(--status-done)',
  digest: 'var(--accent)',
}

function NotificationsPopover({
  items,
  onClose,
  onMarkAllRead,
  onDismiss,
  onViewActivity,
}: {
  items: Notification[]
  onClose: () => void
  onMarkAllRead: () => void
  onDismiss: (id: string) => void
  onViewActivity: () => void
}) {
  return (
    <div className="ws-notif-scrim" onClick={onClose}>
      <div className="ws-notif" onClick={(e) => e.stopPropagation()}>
        <div className="ws-notif__head">
          <span className="ws-notif__title">Notifications</span>
          <span className="ws-notif__markall" onClick={onMarkAllRead}>
            Mark all read
          </span>
        </div>
        <div className="ws-notif__list vscroll">
          {items.map((n) => (
            <div key={n.id} className={`ws-notif__row${n.read ? '' : ' ws-notif__row--unread'}`}>
              <span className="ws-notif__dot" style={{ background: NOTIF_DOT[n.kind] ?? 'var(--accent)' }} />
              <div className="ws-notif__body">
                <div className="ws-notif__row-title">{n.title}</div>
                <div className="ws-notif__row-sub">{n.body}</div>
              </div>
              <div className="ws-notif__aside">
                <span className="ws-notif__time">{n.time}</span>
                <span className="ws-notif__x" onClick={() => onDismiss(n.id)}>
                  ×
                </span>
              </div>
            </div>
          ))}
          {items.length === 0 && <div className="ws-notif__empty">You’re all caught up.</div>}
        </div>
        <div className="ws-notif__foot" onClick={onViewActivity}>
          View all activity →
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------- activity / curator

const ACT_DOT: Record<string, string> = {
  tag: 'var(--accent)',
  link: 'var(--accent)',
  organize: 'var(--accent)',
  summary: 'var(--accent)',
  move: 'var(--accent)',
  archive: 'var(--status-backlog)',
  write: 'var(--status-inprogress)',
  read: 'var(--status-done)',
  search: 'var(--status-done)',
  delete: 'var(--danger)',
  error: 'var(--danger)',
}

function ActivityErrorRow({
  ev,
  onCopy,
}: {
  ev: ActivityEvent
  onCopy: (text: string, label: string) => void
}) {
  const [open, setOpen] = useState(false)
  const json = JSON.stringify(
    {
      id: ev.id,
      level: ev.level ?? 'error',
      tool: ev.tool,
      status: ev.status,
      message: ev.detail,
      actor: ev.actor,
      session: ev.session,
      time: ev.time,
    },
    null,
    2,
  )
  return (
    <div className={`ws-err${open ? ' ws-err--open' : ''}`}>
      <div className="ws-err__head" onClick={() => setOpen((o) => !o)}>
        <span className="ws-activity__dot" style={{ background: 'var(--danger)' }} />
        <div className="ws-err__main">
          <div className="ws-err__tags">
            <span className="ws-err__level">{ev.level ?? 'error'}</span>
            {ev.tool && <span className="ws-err__tool">{ev.tool}</span>}
            {ev.status ? <span className="ws-err__status">{ev.status}</span> : null}
          </div>
          <div className="ws-err__msg">{ev.detail}</div>
          <div className="ws-err__meta">
            {ev.actor} · {ev.session ?? 'server'}
          </div>
        </div>
        <div className="ws-err__aside">
          <span className="ws-activity__time">{ev.time}</span>
          <span className="ws-err__chev">{open ? '▾' : '▸'}</span>
        </div>
      </div>
      {open && (
        <div className="ws-err__body">
          <div className="ws-err__label">Details</div>
          <pre className="ws-err__pre vscroll">{ev.detail}</pre>
          <div className="ws-err__actions">
            <button className="ws-err__export" onClick={() => onCopy(json, 'Error JSON')}>
              ⤓ Export error → JSON
            </button>
            <button
              className="ws-err__fix"
              onClick={() =>
                onCopy(
                  `Fix this vellum MCP error — tool ${ev.tool ?? '?'}, status ${ev.status ?? '?'}: ${ev.detail}`,
                  'Fix prompt',
                )
              }
            >
              ✦ Fix with Claude Code
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

function ActivityDrawer({
  data,
  filter,
  totalNotes,
  search,
  onSearch,
  onFilter,
  onRunCurator,
  onClose,
  showToast,
}: {
  data: ActivityData | null
  filter: 'all' | 'mcp' | 'curator' | 'errors'
  totalNotes: number
  search: string
  onSearch: (q: string) => void
  onFilter: (f: 'all' | 'mcp' | 'curator' | 'errors') => void
  onRunCurator: () => void
  onClose: () => void
  showToast: (text: string, tone?: 'ok' | 'danger') => void
}) {
  const cur = data?.curator
  const errorCount = data?.errorCount ?? 0
  const q = search.trim().toLowerCase()
  const events = (data?.events ?? []).filter((ev) => {
    if (!q) return true
    return [ev.actor, ev.verb, ev.target, ev.detail, ev.tool].some((s) => s?.toLowerCase().includes(q))
  })
  const copy = (text: string, label: string) => {
    void navigator.clipboard?.writeText(text).then(
      () => showToast(`${label} copied`),
      () => showToast('Copy failed', 'danger'),
    )
  }
  const TABS: [typeof filter, string][] = [
    ['all', 'All'],
    ['mcp', 'MCP'],
    ['curator', 'Curator'],
    ['errors', 'Errors'],
  ]
  return (
    <div className="ws-drawer-scrim" onClick={onClose}>
      <div className="ws-drawer ws-drawer--wide vscroll" onClick={(e) => e.stopPropagation()}>
        <div className="ws-drawer__head">
          <div className="ws-drawer__head-row">
            <div>
              <div className="ws-drawer__title">Activity &amp; errors</div>
              <div className="ws-drawer__sub">Curator, client calls and anything that broke in MCP.</div>
            </div>
            <button className="ws-drawer__esc" onClick={onClose}>
              esc
            </button>
          </div>
          <div className="ws-curator">
            <div className="ws-curator__top">
              <div className="ws-curator__avatar">
                <Icon name="sparkle" size={18} />
              </div>
              <div className="ws-curator__id">
                <div className="ws-curator__name-row">
                  <span className="ws-curator__name">Curator</span>
                  <span className={`ws-curator__badge${cur?.enabled ? '' : ' ws-curator__badge--off'}`}>
                    <span className="ws-curator__badge-dot" />
                    {cur?.enabled ? 'watching' : 'off'}
                  </span>
                </div>
                <div className="ws-curator__sub">Auto-tags, links and tidies your vault.</div>
              </div>
              <button className="ws-curator__run" onClick={onRunCurator}>
                Run now
              </button>
            </div>
            <div className="ws-curator__divider" />
            <div className="ws-curator__meta">
              <span>ran {cur?.lastRun ?? 'never'}</span>
              <span className="ws-curator__mid-dot">·</span>
              <span>{cur?.changes ?? 0} changes today</span>
              <span className="ws-curator__mid-dot">·</span>
              <span>watching {cur?.watching ?? totalNotes} notes</span>
            </div>
          </div>
          <div className="ws-activity__search">
            <Icon name="search" size={14} className="ws-activity__search-icon" />
            <input
              className="ws-activity__search-input"
              value={search}
              onChange={(e) => onSearch(e.target.value)}
              placeholder="Search activity, errors, tools…"
            />
            {search && (
              <span className="ws-activity__search-clear" onClick={() => onSearch('')}>
                ×
              </span>
            )}
          </div>
          <div className="ws-segmented ws-activity__filter">
            {TABS.map(([f, label]) => (
              <button
                key={f}
                className={`ws-segmented__item${filter === f ? ' ws-segmented__item--active' : ''}`}
                onClick={() => onFilter(f)}
              >
                {label}
                {f === 'errors' && errorCount > 0 && <span className="ws-activity__err-count">{errorCount}</span>}
              </button>
            ))}
          </div>
        </div>
        <div className="ws-drawer__body ws-activity__timeline">
          {events.map((ev) =>
            ev.isError ? (
              <ActivityErrorRow key={ev.id} ev={ev} onCopy={copy} />
            ) : (
              <div key={ev.id} className={`ws-activity__row${ev.pending ? ' ws-activity__row--pending' : ''}`}>
                <span className="ws-activity__dot" style={{ background: ACT_DOT[ev.kind] ?? 'var(--accent)' }} />
                <div className="ws-activity__main">
                  <div className="ws-activity__line">
                    <span
                      className={`ws-activity__actor${ev.source === 'curator' ? ' ws-activity__actor--curator' : ''}`}
                    >
                      {ev.actor}
                    </span>
                    <span className="ws-activity__verb"> {ev.verb} </span>
                    <span className="ws-activity__target">{ev.target}</span>
                  </div>
                  {ev.detail && <div className="ws-activity__detail">{ev.detail}</div>}
                  {ev.pending && <span className="ws-activity__review">needs review →</span>}
                </div>
                <span className="ws-activity__time">{ev.time}</span>
              </div>
            ),
          )}
          {events.length === 0 && (
            <div className="ws-drawer__empty">
              <div className="ws-drawer__empty-glyph">∅</div>
              <div className="ws-drawer__empty-title">Nothing yet</div>
              <div className="ws-drawer__empty-sub">No activity for this filter.</div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
