# MCP interface reference

Everything vellum exposes over the Model Context Protocol: transports,
the handshake, all tools with their annotations, resources and
subscriptions. The Go source of truth is `internal/mcpserver/`
(`server.go`, `curator.go`, `resources.go`).

## Transports

| Transport | Where | Use |
| --- | --- | --- |
| Streamable HTTP | `POST/GET https://<host>/mcp` | Production. Stateful sessions (`Mcp-Session-Id` header), SSE responses. Behind OAuth 2.1 when `AUTH_ENABLED=true` and an origin check (see [threat-model.md](threat-model.md)). |
| stdio | `vellum --mcp-stdio` | Local use without a server: `claude mcp add vellum -- vellum --mcp-stdio` (set `VELLUM_VAULT_PATH`). No auth — it's your own process. |

The server negotiates any protocol version the official
[go-sdk](https://github.com/modelcontextprotocol/go-sdk) supports
(currently up to `2025-11-25`); nothing is pinned.

## Handshake

`initialize` returns, besides the server info (name, title, version,
website URL, an inline SVG icon):

- **Capabilities**: `tools` (+`listChanged`), `resources`
  (+`listChanged`, +`subscribe`). `logging` is deliberately **not**
  advertised — vellum never emits log messages over MCP.
- **Instructions** — a short guide to the vault conventions, injected by
  clients into the model's context. It covers: the inbox flow
  (`write_note` without a path), `expected_hash` optimistic concurrency,
  the search semantics, `[[wikilinks]]`, task statuses, and the resource
  URI scheme. When the curator is enabled it adds a line explaining that
  curator tools only return context. If you change vault behavior, keep
  `instructions()` in `server.go` in sync — it is the first thing an
  agent reads.

## Tools

All tools are **closed-world** (`openWorldHint: false` — they touch
nothing beyond the vault directory) and carry a human-readable `title`.
Read-only tools set `readOnlyHint: true`. For mutating tools:

- `destructiveHint: true` marks tools that can **discard existing note
  content**: `delete_note`, `write_note` (overwrite replaces the whole
  note), `patch_note` (replaces a section wholesale).
- Metadata-only, trivially reversible edits (tags, status) and `move_note`
  (refuses existing targets, so content is never lost) are
  non-destructive.
- `idempotentHint: true` marks calls whose repetition leaves the vault as
  the first call left it (`append`/`prepend` are the notable exceptions —
  repeating them duplicates content).

Clients use these hints for permission UX: reads can be auto-allowed,
and only genuinely destructive calls need a warning.

### Core tools (always registered)

| Tool | Title | Hints | Summary |
| --- | --- | --- | --- |
| `list_notes` | List notes | read-only | List notes, optional `dir` + `recursive` |
| `read_note` | Read note | read-only | Content, frontmatter, tags, links, content hash |
| `write_note` | Write note | destructive, idempotent | Create/replace; empty path → inbox; returns resolved path + hash |
| `patch_note` | Patch section | destructive, idempotent | Replace the content under one heading |
| `append_to_note` | Append to note | additive | Append markdown to the end |
| `prepend_to_note` | Prepend to note | additive | Insert at top of body (after frontmatter) |
| `delete_note` | Delete note | destructive, idempotent | Delete a note |
| `move_note` | Move note | — | Move/rename; backlinks follow; refuses existing targets |
| `search_notes` | Search vault | read-only | Ranked, diacritics- and typo-insensitive; tag AND-filter, dir scope, regex |
| `list_tags` | List tags | read-only | All tags with note counts |
| `add_tags` | Add tags | idempotent | Add frontmatter tags |
| `remove_tags` | Remove tags | idempotent | Remove frontmatter tags (inline `#tags` untouched) |
| `get_backlinks` | Get backlinks | read-only | Incoming + outgoing resolved links |
| `set_status` | Set task status | idempotent | `backlog \| in-progress \| done`; only status/type lines change |
| `list_tasks` | List tasks | read-only | Task notes, filter by status/project |

### Curator tools (`VELLUM_CURATOR=on`, default off)

All read-only. They return **ranked context, never actions** — the agent
reads the suggestions, decides, and acts through the regular write/move/
tag tools. vellum itself never calls an LLM.

| Tool | Title | Summary |
| --- | --- | --- |
| `suggest_location` | Suggest location | Rank folders for new content by tag overlap |
| `suggest_tags` | Suggest tags | Excerpt, current tags, vault vocabulary, linked-note tags |
| `suggest_links` | Suggest links | Unreciprocated backlinks, title mentions, shared tags |
| `find_untagged` | Find untagged notes | Notes without tags |
| `find_orphans` | Find orphan notes | Notes with no links either direction |
| `find_inbox_stale` | Find stale inbox notes | Inbox notes untouched ≥ N days (default 14) |

### Structured output

Every tool returns `structuredContent` validated against an
auto-generated output schema (from the Go result types), plus the same
JSON serialized in a `TextContent` block for clients that predate
structured output.

### Conflict-safe writes

`read_note` (and every write) returns a sha256 content `hash`. Pass it as
`expected_hash` on the next mutation of the same note; a mismatch fails
with a tool-level `hash mismatch` error instead of clobbering a
concurrent edit. Recovery: `read_note` again, reapply, retry. Without
`expected_hash` writes are last-write-wins.

## Resources

Notes double as MCP resources, so clients with a resource picker can
attach a note as context without a tool call.

- **URI scheme**: `vellum://note/{vault path}`, e.g.
  `vellum://note/projects/x/note.md`. Paths are percent-encoded per
  segment (spaces and diacritics survive).
- **`resources/list`** mirrors the metadata index: one entry per note
  with `name` (the path), `title` (the note title), `mimeType:
  text/markdown` and `size`. The list is paginated by the SDK.
- **`resources/templates/list`** advertises `vellum://note/{+path}` — any
  vault path is addressable even before it appears in the list.
- **`resources/read`** returns the raw markdown (including frontmatter)
  as `text/markdown`. A path that is not a note answers with JSON-RPC
  error `-32002` (resource not found) — same for malformed URIs, so the
  handler leaks nothing about path validation.

```sh
# read a note as a resource (session setup omitted)
curl -s https://vellum.example.com/mcp \
  -H "Authorization: Bearer $TOKEN" -H "Mcp-Session-Id: $SID" \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"resources/read",
       "params":{"uri":"vellum://note/inbox/welcome.md"}}'
```

### Subscriptions

Clients can `resources/subscribe` to a note URI and receive
`notifications/resources/updated` on the session's standalone SSE stream
whenever that note changes — **through any door**: an MCP tool call, the
REST API, or the web editor's autosave all funnel through the same
metadata index, whose change hook fans out to subscribers
(`internal/vault/index.go` → `Index.OnChange`).

- Subscribing to a note that does not exist yet is allowed
  (watch-then-create); subscribing to a non-note URI is rejected.
- Deletion of a subscribed note also emits `resources/updated` — the
  client re-reads, gets `-32002`, and knows it is gone.
- `notifications/resources/list_changed` fires only on create, delete and
  title change. Routine content saves (the web editor autosaves every
  second while typing) do **not** re-announce the list — subscribers of
  the individual note still get every `resources/updated`.
- Notifications are per-session and in-memory: they don't survive a
  server restart, and there is no replay (`Last-Event-ID` is not
  supported). Re-subscribe after a reconnect.

## Client setup

```sh
# Claude Code, remote server (OAuth)
claude mcp add --transport http vellum https://vellum.example.com/mcp

# Claude Code, local vault over stdio (no server, no auth)
VELLUM_VAULT_PATH=~/vault claude mcp add vellum -- vellum --mcp-stdio

# claude.ai custom connector: URL https://vellum.example.com/mcp,
# client ID `vellum`, your VELLUM_CLIENT_SECRET.

# MCP Inspector against a local dev server
npx -y @modelcontextprotocol/inspector
```

In the Inspector the annotations show up per tool, the Resources tab
lists the vault, and subscribing to a note then editing it in the web UI
demonstrates the live `resources/updated` flow.
