import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  ApiClient,
  ConflictError,
  AuthError,
  relativeAge,
  type Note,
  type NoteEntry,
  type SearchResult,
  type TagCount,
} from '../lib/api'
import { CommandPalette, PaletteItem, Highlight } from '../components/palette'

type TypeFilter = 'all' | 'task' | 'knowledge'
type ViewMode = 'edit' | 'split' | 'preview'

const statusDotColor: Record<string, string> = {
  backlog: 'var(--status-backlog)',
  'in-progress': 'var(--status-inprogress)',
  done: 'var(--status-done)',
}

function noteType(e: NoteEntry): 'task' | 'knowledge' {
  return e.type === 'task' ? 'task' : 'knowledge'
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
  const [theme, setTheme] = useState<'light' | 'dark'>('light')

  const refresh = useCallback(async () => {
    try {
      const [notes, tagList] = await Promise.all([api.listNotes(), api.tags()])
      setEntries(notes)
      setTags(tagList)
    } catch (err) {
      if (!(err instanceof AuthError)) console.error(err)
    }
  }, [api])

  useEffect(() => {
    void refresh()
  }, [refresh])

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault()
        setPaletteOpen((o) => !o)
      }
      if (e.key === 'Escape') setPaletteOpen(false)
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [])

  const toggleTheme = () => {
    const next = theme === 'light' ? 'dark' : 'light'
    setTheme(next)
    document.documentElement.setAttribute('data-theme', next)
  }

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

  const projectDirs = useMemo(() => {
    const dirs = new Set<string>()
    for (const e of entries) {
      const m = e.path.match(/^projects\/([^/]+)\//)
      if (m) dirs.add(`projects/${m[1]}`)
    }
    return [...dirs].sort()
  }, [entries])

  // ---- filtered list ----
  const listEntries = useMemo(() => {
    return entries
      .filter((e) => inDir(e, selectedDir))
      .filter((e) => typeFilter === 'all' || noteType(e) === typeFilter)
      .filter((e) => statusFilter === '' || e.status === statusFilter)
      .filter((e) => activeTags.every((t) => e.tags?.includes(t)))
      .sort((a, b) => b.modTime - a.modTime)
  }, [entries, selectedDir, typeFilter, statusFilter, activeTags, inDir])

  const openNote = (path: string) => {
    setPaletteOpen(false)
    navigate(`/n/${path}`)
  }

  const createNote = async () => {
    const dir = selectedDir || 'inbox'
    let name = 'untitled.md'
    for (let i = 2; entries.some((e) => e.path === `${dir}/${name}`); i++) {
      name = `untitled-${i}.md`
    }
    const path = `${dir}/${name}`
    await api.putNote(path, '# Untitled\n')
    await refresh()
    openNote(path)
  }

  return (
    <div className="ws">
      <TopBar
        version={version}
        activeTags={activeTags}
        onRemoveTag={(t) => setActiveTags(activeTags.filter((x) => x !== t))}
        onOpenPalette={() => setPaletteOpen(true)}
        onToggleTheme={toggleTheme}
      />
      <div className="ws-body">
        <TreePanel
          counts={counts}
          projectDirs={projectDirs}
          selectedDir={selectedDir}
          onSelectDir={setSelectedDir}
          tags={tags}
          activeTags={activeTags}
          onToggleTag={(t) =>
            setActiveTags(
              activeTags.includes(t) ? activeTags.filter((x) => x !== t) : [...activeTags, t],
            )
          }
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
          onCreate={createNote}
        />
        <EditorPanel key={selectedPath} api={api} path={selectedPath} onSaved={refresh} />
      </div>
      <footer className="ws-statusbar">
        {selectedPath ? (
          <span>
            <span className="ws-statusbar__dir">
              {selectedPath.includes('/') ? selectedPath.slice(0, selectedPath.lastIndexOf('/') + 1) : ''}
            </span>
            <span className="ws-statusbar__file">
              {selectedPath.split('/').pop()}
            </span>
          </span>
        ) : (
          <span className="ws-statusbar__dir">{selectedDir || 'vault'}</span>
        )}
        <span className="ws-statusbar__spacer" />
        <span>{entries.length} notes</span>
        <span className="ws-statusbar__sep">·</span>
        <span>md</span>
      </footer>
      {paletteOpen && (
        <PaletteOverlay api={api} onClose={() => setPaletteOpen(false)} onOpen={openNote} />
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
}: {
  version: string
  activeTags: string[]
  onRemoveTag: (t: string) => void
  onOpenPalette: () => void
  onToggleTheme: () => void
}) {
  return (
    <header className="ws-topbar">
      <span className="ws-topbar__wordmark">vellum</span>
      <span className="ws-topbar__divider" />
      <button
        className="ws-topbar__search"
        onClick={onOpenPalette}
        style={{ font: 'inherit' }}
      >
        <span className="ws-topbar__search-icon">⌕</span>
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
      <span className="ws-topbar__version">{version}</span>
      <button className="ws-topbar__settings" onClick={onToggleTheme} title="Toggle theme">
        ⚙
      </button>
    </header>
  )
}

// ---------------------------------------------------------------- tree

function TreePanel({
  counts,
  projectDirs,
  selectedDir,
  onSelectDir,
  tags,
  activeTags,
  onToggleTag,
}: {
  counts: Map<string, number>
  projectDirs: string[]
  selectedDir: string
  onSelectDir: (dir: string) => void
  tags: TagCount[]
  activeTags: string[]
  onToggleTag: (t: string) => void
}) {
  const row = (dir: string, label: string, opts: { badge?: boolean; glyph?: string } = {}) => {
    const selected = selectedDir === dir
    const count = counts.get(dir) ?? 0
    return (
      <div
        className={`ws-tree__row${selected ? ' ws-tree__row--selected' : ''}`}
        onClick={() => onSelectDir(selected ? '' : dir)}
      >
        <span className="ws-tree__caret">{selected ? '▾' : '▸'}</span>
        <span className="ws-tree__glyph">{opts.glyph ?? (selected ? '▤' : '▢')}</span>
        <span className="ws-tree__name">{label}</span>
        {opts.badge && count > 0 ? (
          <span className="ws-tree__badge">{count}</span>
        ) : (
          <span className="ws-tree__count">{count}</span>
        )}
      </div>
    )
  }

  return (
    <aside className="ws-tree vscroll">
      <div className="ws-tree__label">Vault</div>
      {row('inbox', 'Inbox', { badge: true })}
      {row('projects', 'Projects')}
      <div style={{ paddingLeft: 20 }}>
        {projectDirs.map((dir) => {
          const active = selectedDir === dir
          return (
            <div
              key={dir}
              className={`ws-tree__row ws-tree__child${active ? ' ws-tree__child--active' : ''}`}
              onClick={() => onSelectDir(active ? 'projects' : dir)}
            >
              <span style={{ width: 10 }} />
              <span className="ws-tree__glyph" style={active ? { color: 'var(--accent)' } : undefined}>
                ▤
              </span>
              <span className="ws-tree__name">{dir.split('/')[1]}</span>
              <span className="ws-tree__count">{counts.get(dir) ?? 0}</span>
            </div>
          )
        })}
      </div>
      {row('archive', 'Archive')}
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
  onCreate,
}: {
  entries: NoteEntry[]
  selectedDir: string
  selectedPath: string
  typeFilter: TypeFilter
  statusFilter: string
  onTypeFilter: (t: TypeFilter) => void
  onStatusFilter: (s: string) => void
  onOpen: (path: string) => void
  onCreate: () => void
}) {
  const crumb = selectedDir === '' ? 'vault' : selectedDir.split('/').join(' / ')
  return (
    <section className="ws-list">
      <div className="ws-list__header">
        <div className="ws-list__crumb-row">
          <span className="ws-list__crumb">{crumb}</span>
          <button className="ws-list__add" onClick={onCreate} title="New note">
            ＋
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
              onClick={() => onTypeFilter(key)}
            >
              {marker}
              {label}
            </button>
          ))}
        </div>
        <div className="ws-segmented">
          {(
            [
              ['', 'All'],
              ['backlog', 'Backlog'],
              ['in-progress', 'In progress'],
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
      </div>
      <div className="ws-list__body vscroll">
        {entries.map((e) => (
          <div
            key={e.path}
            className={`ws-note-row${e.path === selectedPath ? ' ws-note-row--active' : ''}`}
            onClick={() => onOpen(e.path)}
          >
            <div className="ws-note-row__title-line">
              {noteType(e) === 'task' ? (
                <span
                  className="ws-note-row__dot"
                  style={{ background: statusDotColor[e.status ?? 'backlog'] ?? 'var(--status-backlog)' }}
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
      </div>
    </section>
  )
}

// ---------------------------------------------------------------- editor

function EditorPanel({
  api,
  path,
  onSaved,
}: {
  api: ApiClient
  path: string
  onSaved: () => void
}) {
  const [note, setNote] = useState<Note | null>(null)
  const [draft, setDraft] = useState('')
  const [etag, setEtag] = useState('')
  const [dirty, setDirty] = useState(false)
  const [mode, setMode] = useState<ViewMode>('split')
  const [showFrontmatter, setShowFrontmatter] = useState(false)
  const [conflict, setConflict] = useState<{ content: string; etag: string } | null>(null)
  const saveTimer = useRef<number | undefined>(undefined)

  useEffect(() => {
    if (!path) return
    let cancelled = false
    void api.getNote(path).then((n) => {
      if (cancelled) return
      setNote(n)
      setDraft(n.content)
      setEtag(n.hash)
      setDirty(false)
    })
    return () => {
      cancelled = true
    }
  }, [api, path])

  const save = useCallback(
    async (content: string, ifMatch: string) => {
      try {
        const newTag = await api.putNote(path, content, ifMatch)
        setEtag(newTag)
        setDirty(false)
        onSaved()
      } catch (err) {
        if (err instanceof ConflictError) {
          setConflict({ content: err.content, etag: err.etag })
        } else if (!(err instanceof AuthError)) {
          console.error(err)
        }
      }
    },
    [api, path, onSaved],
  )

  const onEdit = (value: string) => {
    setDraft(value)
    setDirty(true)
    window.clearTimeout(saveTimer.current)
    saveTimer.current = window.setTimeout(() => void save(value, etag), 1200)
  }

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 's') {
        e.preventDefault()
        window.clearTimeout(saveTimer.current)
        void save(draft, etag)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [draft, etag, save])

  if (!path || !note) {
    return (
      <main className="ws-editor">
        <div className="ws-editor__empty">
          <div className="v-empty" style={{ border: 'none', background: 'transparent' }}>
            <div className="v-empty__glyph">∅</div>
            <div className="v-empty__title">No note open</div>
            <div className="v-empty__body">Pick a note from the list or press ⌘K.</div>
          </div>
        </div>
      </main>
    )
  }

  const fm = note.frontmatter ?? {}
  const type = fm['type'] === 'task' ? 'task' : 'knowledge'
  const status = typeof fm['status'] === 'string' ? (fm['status'] as string) : ''
  const draftBody = stripFrontmatter(draft)
  const frontmatterBlock = draft.slice(0, draft.length - draftBody.length)

  return (
    <main className="ws-editor">
      <div className="ws-editor__header">
        <div className="ws-editor__title-row">
          <h1 className="ws-editor__title">{note.title}</h1>
          <div className="ws-segmented ws-editor__modes">
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
        </div>
        <div className="ws-editor__meta">
          <span className="ws-type-badge">
            {type === 'task' ? (
              <span
                className="ws-note-row__dot"
                style={{ background: statusDotColor[status] ?? 'var(--status-backlog)' }}
              />
            ) : (
              <span className="ws-note-row__square" style={{ width: 7, height: 7 }} />
            )}
            {type}
          </span>
          {status && (
            <span className={`v-status-badge v-status-badge--${status}`} style={{ fontSize: 12 }}>
              <span
                className="ws-note-row__dot"
                style={{ background: statusDotColor[status] }}
              />
              {status === 'in-progress' ? 'In progress' : status[0].toUpperCase() + status.slice(1)}
            </span>
          )}
          {(note.tags ?? []).map((t) => (
            <span key={t} className="ws-meta-tag">
              #{t}
            </span>
          ))}
          <span style={{ flex: 1 }} />
          <span className="ws-editor__modified">
            {dirty ? 'unsaved' : `modified ${relativeAge(Date.parse(note.modTime) / 1000)} ago`}
          </span>
          {frontmatterBlock && (
            <button
              className="ws-editor__frontmatter"
              onClick={() => setShowFrontmatter(!showFrontmatter)}
            >
              {showFrontmatter ? '▴' : '▾'} frontmatter
            </button>
          )}
        </div>
      </div>
      <div className="ws-editor__body">
        {mode !== 'preview' && (
          <textarea
            className="ws-editor__raw vscroll"
            value={draft}
            onChange={(e) => onEdit(e.target.value)}
            spellCheck={false}
          />
        )}
        {mode !== 'edit' && (
          <div className="ws-editor__preview vscroll">
            {showFrontmatter && frontmatterBlock && (
              <pre className="v-markdown" style={{ marginBottom: 18 }}>
                {frontmatterBlock.trim()}
              </pre>
            )}
            <MarkdownView body={draftBody} />
          </div>
        )}
      </div>
      {conflict && (
        <div className="ws-overlay" style={{ alignItems: 'center', paddingTop: 0 }}>
          <div className="v-modal" style={{ width: 440 }}>
            <div className="v-modal__title">Note changed on disk</div>
            <div className="v-modal__body">
              Someone else saved <span className="v-modal__code">{path}</span> while you were
              editing. Load the latest version, or overwrite it with yours.
            </div>
            <div className="v-modal__actions">
              <button
                className="v-modal__cancel"
                onClick={() => {
                  setDraft(conflict.content)
                  setEtag(conflict.etag)
                  setDirty(false)
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

function stripFrontmatter(content: string): string {
  if (!content.startsWith('---\n')) return content
  const end = content.indexOf('\n---\n', 4)
  if (end < 0) return content
  return content.slice(end + 5)
}

/** Markdown preview with GFM, design checkboxes and wikilink chips. */
function MarkdownView({ body }: { body: string }) {
  const navigate = useNavigate()
  // [[wikilink]] / [[wikilink|alias]] → placeholder links resolved on click.
  const processed = body.replace(
    /\[\[([^\][|]+)(?:\|([^\][]+))?\]\]/g,
    (_, target: string, alias?: string) =>
      `[${alias ?? target}](#wikilink=${encodeURIComponent(target.trim())})`,
  )
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
            return (
              <span className={`v-checkbox${checked ? ' v-checkbox--checked' : ''}`}>
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

  useEffect(() => {
    const t = window.setTimeout(() => {
      const q = query.trim()
      const tags = q
        .split(/\s+/)
        .filter((w) => w.startsWith('#') && w.length > 1)
        .map((w) => w.slice(1))
      const text = q
        .split(/\s+/)
        .filter((w) => !w.startsWith('#'))
        .join(' ')
      void api.search(text, tags).then((r) => {
        setResults(r.slice(0, 8))
        setSelected(0)
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
