// REST client for /api/* (PHY-114). The session lives in localStorage: the 1h
// access token is silently renewed from the 30-day refresh token (rotated on
// every use), so the login survives tab close, browser restart and the hourly
// access-token expiry — up to the refresh token's 30-day lifetime. Notes are
// cached client-side and revalidated with If-None-Match, so reopening a note is
// instant.

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

export class NotFoundError extends Error {
  constructor(path: string) {
    super(`not found: ${path}`)
  }
}

/** A create-only PUT (`createOnly`) landed on a name that is already taken. */
export class ExistsError extends Error {
  constructor(path: string) {
    super(`already exists: ${path}`)
  }
}

const TOKEN_KEY = 'vellum_token'
const REFRESH_KEY = 'vellum_refresh'
const CLIENT_KEY = 'vellum_client'
const DEFAULT_CLIENT_ID = 'vellum'

interface TokenResponse {
  access_token: string
  refresh_token?: string
}

export class ApiClient {
  private token: string | null = null
  private refreshToken: string | null = null
  private clientId = DEFAULT_CLIENT_ID
  /** In-flight refresh, shared so parallel 401s rotate the token only once. */
  private refreshing: Promise<boolean> | null = null
  /** Client-side note cache, revalidated with If-None-Match → 304. */
  private noteCache = new Map<string, Note>()
  private prefetching = new Set<string>()
  onAuthError?: () => void
  /** Fired when the server looks down (network error or 502–504) — the
   * workspace shows the reconnect overlay and polls /healthz. */
  onUnavailable?: () => void

  constructor() {
    // Restore the session so neither a reload nor a browser restart forces a
    // re-login. localStorage (not sessionStorage): it must outlive tab close.
    try {
      this.token = localStorage.getItem(TOKEN_KEY)
      this.refreshToken = localStorage.getItem(REFRESH_KEY)
      this.clientId = localStorage.getItem(CLIENT_KEY) || DEFAULT_CLIENT_ID
    } catch {
      /* private mode / storage disabled */
    }
  }

  /** A restorable session exists if either token is present — an expired access
   * token still refreshes as long as the refresh token is live. */
  hasToken(): boolean {
    return !!(this.token || this.refreshToken)
  }

  setToken(token: string | null) {
    this.token = token
    writeStorage(TOKEN_KEY, token)
  }

  /** Persist a freshly issued access/refresh pair (refresh tokens rotate, so
   * the new one must be stored immediately or the next refresh fails). */
  private setTokens(access: string, refresh: string | null) {
    this.token = access
    this.refreshToken = refresh
    writeStorage(TOKEN_KEY, access)
    writeStorage(REFRESH_KEY, refresh)
  }

  /** Drop the whole session (both tokens + client id). */
  private clearSession() {
    this.token = null
    this.refreshToken = null
    writeStorage(TOKEN_KEY, null)
    writeStorage(REFRESH_KEY, null)
  }

  /** Exchange the client secret for a token pair (client_credentials). */
  async connect(secret: string, clientId = DEFAULT_CLIENT_ID): Promise<void> {
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
    const body = (await res.json()) as TokenResponse
    this.clientId = clientId
    writeStorage(CLIENT_KEY, clientId)
    this.setTokens(body.access_token, body.refresh_token ?? null)
  }

  /** Renew the access token from the stored refresh token. Concurrent callers
   * share one in-flight request. Returns false (and clears the session) when
   * the refresh token is missing or rejected; a network failure leaves the
   * session intact so a transient outage isn't mistaken for a logout. */
  private async refresh(): Promise<boolean> {
    if (!this.refreshToken) return false
    if (this.refreshing) return this.refreshing
    this.refreshing = (async () => {
      try {
        const res = await fetch('/token', {
          method: 'POST',
          headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
          body: new URLSearchParams({
            grant_type: 'refresh_token',
            client_id: this.clientId,
            refresh_token: this.refreshToken as string,
          }),
        })
        if (!res.ok) {
          this.clearSession() // refresh token expired/revoked — real logout
          return false
        }
        const body = (await res.json()) as TokenResponse
        this.setTokens(body.access_token, body.refresh_token ?? this.refreshToken)
        return true
      } catch {
        return false // server unreachable — keep the session, retry later
      } finally {
        this.refreshing = null
      }
    })()
    return this.refreshing
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
    if (res.status === 404) {
      this.evictNotes(path, true) // deleted on the server — drop the stale copy
      throw new NotFoundError(path)
    }
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

  /**
   * PUT with optional If-Match; throws ConflictError on 409. With
   * `createOnly` the write is refused when the path already exists
   * (ExistsError) instead of overwriting a note we never loaded.
   */
  async putNote(
    path: string,
    content: string,
    etag?: string,
    opts?: { keepalive?: boolean; createOnly?: boolean },
  ): Promise<string> {
    const headers: Record<string, string> = { 'Content-Type': 'text/markdown' }
    if (etag) headers['If-Match'] = `"${etag}"`
    if (opts?.createOnly) headers['If-None-Match'] = '*'
    const res = await this.rawRequest(
      'PUT',
      `/api/notes/${encodePath(path)}`,
      content,
      headers,
      opts?.keepalive,
    )
    if (res.status === 409) {
      const body = (await res.json()) as { content: string; etag: string }
      throw new ConflictError(body.content, body.etag)
    }
    if (res.status === 412) throw new ExistsError(path)
    if (!res.ok) throw new Error(`save failed (${res.status})`)
    const body = (await res.json()) as { etag: string }
    this.evictNotes(path, true) // next GET refetches the saved note
    return body.etag
  }

  async deleteNote(path: string): Promise<void> {
    await this.request('DELETE', `/api/notes/${encodePath(path)}`)
    this.evictNotes(path, true)
  }

  /** Move/rename; throws ExistsError when the destination is taken. */
  async moveNote(from: string, to: string): Promise<void> {
    const res = await this.rawRequest('POST', '/api/notes/move', JSON.stringify({ from, to }), {
      'Content-Type': 'application/json',
    })
    if (res.status === 409) throw new ExistsError(to)
    if (!res.ok) throw new Error(`move ${from} → ${to}: ${res.status}`)
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
    // keepalive lets a save started during pagehide outlive the page
    // (refresh, tab close). Browsers cap keepalive bodies at ~64 KB, so it
    // is opt-in for the draft flush only.
    keepalive = false,
    // set once we've already renewed the token for this request, to bound the
    // 401 → refresh → replay to a single retry.
    retried = false,
  ): Promise<Response> {
    if (this.token) headers['Authorization'] = `Bearer ${this.token}`
    let res: Response
    try {
      res = await fetch(url, { method, body, headers, keepalive })
    } catch (err) {
      // fetch only rejects on network-level failure — the server (or the
      // proxy in front of it) is down, most likely a deploy restart.
      this.onUnavailable?.()
      throw err
    }
    if (res.status >= 502 && res.status <= 504) {
      this.onUnavailable?.()
      throw new Error(`${method} ${url}: ${res.status}`)
    }
    if (res.status === 401) {
      // The access token expired (or the server dropped it). Silently renew
      // from the refresh token and replay once before surfacing a logout.
      if (!retried && (await this.refresh())) {
        return this.rawRequest(method, url, body, headers, keepalive, true)
      }
      this.clearSession()
      this.onAuthError?.()
      throw new AuthError()
    }
    return res
  }
}

function encodePath(path: string): string {
  return path.split('/').map(encodeURIComponent).join('/')
}

/** Write (or, for a null value, remove) a localStorage key, ignoring failures
 * in private mode / when storage is disabled. */
function writeStorage(key: string, value: string | null): void {
  try {
    if (value) localStorage.setItem(key, value)
    else localStorage.removeItem(key)
  } catch {
    /* ignore */
  }
}

export function relativeAge(unixSeconds: number): string {
  const diff = Date.now() / 1000 - unixSeconds
  if (diff < 3600) return `${Math.max(1, Math.floor(diff / 60))}m`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h`
  if (diff < 7 * 86400) return `${Math.floor(diff / 86400)}d`
  if (diff < 30 * 86400) return `${Math.floor(diff / (7 * 86400))}w`
  return `${Math.floor(diff / (30 * 86400))}mo`
}
