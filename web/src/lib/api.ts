// REST client for /api/* (PHY-114). Token lives in memory only — no
// localStorage/sessionStorage by design.

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
  source: 'mcp' | 'curator' | 'user'
  actor: string
  kind: string
  verb: string
  target: string
  detail: string
  pending?: boolean
  time: string
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

export class ApiClient {
  private token: string | null = null
  onAuthError?: () => void

  setToken(token: string | null) {
    this.token = token
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
    this.token = body.access_token
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
    return (await this.request('GET', `/api/notes/${encodePath(path)}`)) as Note
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
    return body.etag
  }

  async deleteNote(path: string): Promise<void> {
    await this.request('DELETE', `/api/notes/${encodePath(path)}`)
  }

  async moveNote(from: string, to: string): Promise<void> {
    await this.request('POST', '/api/notes/move', JSON.stringify({ from, to }), {
      'Content-Type': 'application/json',
    })
  }

  async search(q: string, tags: string[] = []): Promise<SearchResult[]> {
    const params = new URLSearchParams()
    if (q) params.set('q', q)
    if (tags.length) params.set('tags', tags.join(','))
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
      this.token = null
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
