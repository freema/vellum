// REST client for /api/* (PHY-114). The token lives in sessionStorage so a
// page refresh keeps the login; notes are cached client-side and revalidated
// with If-None-Match, so reopening a note is instant.

export interface NoteEntry {
  path: string
  title: string
  excerpt?: string
  tags?: string[]
  type?: string
  status?: string
  modTime: number
  size: number
}

export interface Note {
  path: string
  title: string
  content: string
  body: string
  frontmatter?: Record<string, unknown>
  tags?: string[]
  links?: string[]
  hash: string
  modTime: string
}

export interface SearchSnippet {
  line: number
  match: string
  context: string
}

export interface SearchResult {
  path: string
  title: string
  snippets?: SearchSnippet[]
}

export interface TagCount {
  tag: string
  count: number
}

export interface Connection {
  id: string
  name: string
  kind: string
  mono: string
  status: 'active' | 'idle'
  since: string
  lastTool?: string
  lastAgo: string
  calls: number
}

export interface ConnectionsData {
  endpoint: string
  activeCount: number
  totalCalls: number
  connections: Connection[]
}

export interface ActivityEvent {
  id: string
  source: 'mcp' | 'curator' | 'user' | 'system'
  actor: string
  kind: string
  verb: string
  target: string
  detail: string
  pending?: boolean
  time: string
  // error events (kind === "error")
  isError?: boolean
  level?: string
  tool?: string
  status?: number
  session?: string
}

export interface CuratorStatus {
  enabled: boolean
  changes: number
  watching: number
  lastRun: string
}

export interface ActivityData {
  curator: CuratorStatus
  events: ActivityEvent[]
  errorCount: number
}

export interface Notification {
  id: string
  kind: 'curator' | 'task' | 'mcp' | 'digest'
  title: string
  body: string
  time: string
  read: boolean
}

export class ConflictError extends Error {
  content: string
  etag: string

  constructor(content: string, etag: string) {
    super('conflict')
    this.content = content
    this.etag = etag
  }
}

export class AuthError extends Error {
  constructor() {
    super('unauthorized')
  }
}

const TOKEN_KEY = 'vellum_token'

export class ApiClient {
  private token: string | null = null
  /** Client-side note cache, revalidated with If-None-Match → 304. */
  private noteCache = new Map<string, Note>()
  private prefetching = new Set<string>()
  onAuthError?: () => void

  constructor() {
    // Restore the session token so a page refresh doesn't force a re-login.
    // sessionStorage (not localStorage) — survives reload, cleared on tab close.
    try {
      this.token = sessionStorage.getItem(TOKEN_KEY)
    } catch {
      /* private mode / storage disabled */
    }
  }

  hasToken(): boolean {
    return !!this.token
  }

  setToken(token: string | null) {
    this.token = token
    try {
      if (token) sessionStorage.setItem(TOKEN_KEY, token)
      else sessionStorage.removeItem(TOKEN_KEY)
    } catch {
      /* ignore */
    }
  }

  /** Exchange the client secret for a bearer token (client_credentials). */
  async connect(secret: string, clientId = 'vellum'): Promise<void> {
    const res = await fetch('/token', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({
        grant_type: 'client_credentials',
        client_id: clientId,
        client_secret: secret,
      }),
    })
    if (!res.ok) {
      throw new Error(res.status === 401 ? 'Invalid client secret' : `Connect failed (${res.status})`)
    }
    const body = (await res.json()) as { access_token: string }
    this.setToken(body.access_token)
  }

  /** Server health: whether auth is on + the running version. */
  async health(): Promise<{ auth: boolean; version: string }> {
    const res = await fetch('/healthz')
    return (await res.json()) as { auth: boolean; version: string }
  }

  async version(): Promise<string> {
    const body = (await this.request('GET', '/api/version')) as { version: string }
    return body.version
  }

  async listNotes(): Promise<NoteEntry[]> {
    const body = (await this.request('GET', '/api/notes?recursive=true')) as {
      notes: NoteEntry[]
    }
    return body.notes
  }

  async getNote(path: string): Promise<Note> {
    const cached = this.noteCache.get(path)
    const headers: Record<string, string> = {}
    if (cached) headers['If-None-Match'] = `"${cached.hash}"`
    const res = await this.rawRequest('GET', `/api/notes/${encodePath(path)}`, undefined, headers)
    if (res.status === 304 && cached) return cached
    if (!res.ok) throw new Error(`GET note ${path}: ${res.status}`)
    const note = (await res.json()) as Note
    this.cacheNote(note)
    return note
  }

  /** Warm the note cache in the background (e.g. on list-row hover). */
  prefetchNote(path: string): void {
    if (this.noteCache.has(path) || this.prefetching.has(path)) return
    this.prefetching.add(path)
    void this.getNote(path)
      .catch(() => {})
      .finally(() => this.prefetching.delete(path))
  }

  private cacheNote(note: Note): void {
    this.noteCache.delete(note.path) // refresh insertion order
    this.noteCache.set(note.path, note)
    if (this.noteCache.size > 100) {
      const oldest = this.noteCache.keys().next().value
      if (oldest) this.noteCache.delete(oldest)
    }
  }

  private evictNotes(prefix: string, exact = false): void {
    for (const key of this.noteCache.keys()) {
      if (exact ? key === prefix : key === prefix || key.startsWith(prefix + '/')) {
        this.noteCache.delete(key)
      }
    }
  }

  /** PUT with optional If-Match; throws ConflictError on 409. */
  async putNote(path: string, content: string, etag?: string): Promise<string> {
    const headers: Record<string, string> = { 'Content-Type': 'text/markdown' }
    if (etag) headers['If-Match'] = `"${etag}"`
    const res = await this.rawRequest('PUT', `/api/notes/${encodePath(path)}`, content, headers)
    if (res.status === 409) {
      const body = (await res.json()) as { content: string; etag: string }
      throw new ConflictError(body.content, body.etag)
    }
    if (!res.ok) throw new Error(`save failed (${res.status})`)
    const body = (await res.json()) as { etag: string }
    this.evictNotes(path, true) // next GET refetches the saved note
    return body.etag
  }

  async deleteNote(path: string): Promise<void> {
    await this.request('DELETE', `/api/notes/${encodePath(path)}`)
    this.evictNotes(path, true)
  }

  async moveNote(from: string, to: string): Promise<void> {
    await this.request('POST', '/api/notes/move', JSON.stringify({ from, to }), {
      'Content-Type': 'application/json',
    })
    this.evictNotes(from, true)
    this.evictNotes(to, true)
  }

  async search(q: string, tags: string[] = [], limit = 0): Promise<SearchResult[]> {
    const params = new URLSearchParams()
    if (q) params.set('q', q)
    if (tags.length) params.set('tags', tags.join(','))
    if (limit > 0) params.set('limit', String(limit))
    const body = (await this.request('GET', `/api/search?${params}`)) as {
      results: SearchResult[] | null
    }
    return body.results ?? []
  }

  async tags(): Promise<TagCount[]> {
    const body = (await this.request('GET', '/api/tags')) as { tags: TagCount[] }
    return body.tags
  }

  async listFolders(): Promise<string[]> {
    const body = (await this.request('GET', '/api/folders')) as { folders: string[] | null }
    return body.folders ?? []
  }

  async createFolder(path: string): Promise<void> {
    await this.request('POST', '/api/folders', JSON.stringify({ path }), {
      'Content-Type': 'application/json',
    })
  }

  async deleteFolder(path: string): Promise<{ deleted: string; notes: number }> {
    const res = (await this.request('DELETE', `/api/folders/${encodePath(path)}`)) as {
      deleted: string
      notes: number
    }
    this.evictNotes(path) // everything under the folder is gone
    return res
  }

  async connections(): Promise<ConnectionsData> {
    return (await this.request('GET', '/api/connections')) as ConnectionsData
  }

  async revokeConnection(id: string): Promise<void> {
    await this.request('DELETE', `/api/connections/${encodeURIComponent(id)}`)
  }

  async activity(filter = 'all'): Promise<ActivityData> {
    return (await this.request('GET', `/api/activity?filter=${encodeURIComponent(filter)}`)) as ActivityData
  }

  async notifications(): Promise<{ notifications: Notification[]; unread: number }> {
    return (await this.request('GET', '/api/notifications')) as {
      notifications: Notification[]
      unread: number
    }
  }

  async runCurator(): Promise<{ enabled: boolean; changes: number }> {
    return (await this.request('POST', '/api/curator/run')) as { enabled: boolean; changes: number }
  }

  private async request(
    method: string,
    url: string,
    body?: string,
    headers: Record<string, string> = {},
  ): Promise<unknown> {
    const res = await this.rawRequest(method, url, body, headers)
    if (!res.ok) {
      const text = await res.text()
      throw new Error(`${method} ${url}: ${res.status} ${text}`)
    }
    return res.json()
  }

  private async rawRequest(
    method: string,
    url: string,
    body?: string,
    headers: Record<string, string> = {},
  ): Promise<Response> {
    if (this.token) headers['Authorization'] = `Bearer ${this.token}`
    const res = await fetch(url, { method, body, headers })
    if (res.status === 401) {
      this.setToken(null)
      this.onAuthError?.()
      throw new AuthError()
    }
    return res
  }
}

function encodePath(path: string): string {
  return path.split('/').map(encodeURIComponent).join('/')
}

export function relativeAge(unixSeconds: number): string {
  const diff = Date.now() / 1000 - unixSeconds
  if (diff < 3600) return `${Math.max(1, Math.floor(diff / 60))}m`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h`
  if (diff < 7 * 86400) return `${Math.floor(diff / 86400)}d`
  if (diff < 30 * 86400) return `${Math.floor(diff / (7 * 86400))}w`
  return `${Math.floor(diff / (30 * 86400))}mo`
}
