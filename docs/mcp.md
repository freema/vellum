# MCP interface reference

Everything vellum exposes over the Model Context Protocol: transports,
the handshake, all tools with their annotations, resources and
subscriptions. The Go source of truth is `internal/mcpserver/`
(`server.go`, `curator.go`, `resources.go`).

## Transports

| Transport | Where | Use |
| --- | --- | --- |
| Streamable HTTP | `POST/GET https://<host>/mcp` | Production. Stateful sessions (`Mcp-Session-Id` header), SSE responses. Behind OAuth 2.1 when `AUTH_ENABLED=true` and an origin check (see [threat-model.md](threat-model.md)). |
| stdio | `vellum mcp <vault>` | Local use without a server: `claude mcp add vellum -- vellum mcp ~/notes`. The folder must exist (add `-init` to create it); `~/` is expanded. The older `vellum -mcp-stdio` with `VELLUM_VAULT_PATH` still works. No auth: it's your own process. |

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
  URI scheme, and the folder description convention (see
  [Folder descriptions](#folder-descriptions)). When the vault root has a
  description, it closes the instructions (`This vault: ...`, at most 500
  characters). When the curator is enabled it adds a line explaining that
  curator tools only return context. If you change vault behavior, keep
  `instructions()` in `server.go` in sync — it is the first thing an
  agent reads.

## Tools

All tools are **closed-world** (`openWorldHint: false` — they touch
nothing beyond the vault directory) and carry a human-readable `title`.
Read-only tools set `readOnlyHint: true`. For mutating tools:

- `destructiveHint: true` marks tools that can **discard existing note
  content**: `delete_note`, `write_note` (overwrite replaces the whole
  note), `patch_note` (replaces a section wholesale), `replace_in_note`
  (can replace text with less, or with nothing).
- Metadata-only, trivially reversible edits (tags, status) and `move_note`
  (refuses existing targets, so content is never lost) are
  non-destructive.
- `idempotentHint: true` marks calls whose repetition leaves the vault as
  the first call left it (`append`/`prepend` are the notable exceptions —
  repeating them duplicates content; `replace_in_note` is not idempotent
  either, a repeat fails or replaces again, nor is `patch_note`, whose
  `append` and insert operations add again).

Clients use these hints for permission UX: reads can be auto-allowed,
and only genuinely destructive calls need a warning.

### Core tools (always registered)

| Tool | Title | Hints | Summary |
| --- | --- | --- | --- |
| `list_notes` | List notes | read-only | List notes, optional `dir` + `recursive`; at most `limit` (default 200) per call, `offset` to page; `dirs` lists the direct subfolders with their note counts, also without `recursive`; `total`, `truncated` and a `hint` when cut; `sort: modified` and `since` for recent changes (see [What changed recently](#what-changed-recently)) |
| `read_note` | Read note | read-only | Content, frontmatter, tags, links, content hash |
| `read_notes` | Read notes | read-only | Up to 20 notes in one call, by `paths` or a glob `pattern` (`projects/x/*.md`); per-note content, hash, tags and title, or an error, or `skipped` when over `max_bytes_per_note` (10 KB) or `max_total_bytes` (60 KB); `outline_only` for outlines |
| `write_note` | Write note | destructive, idempotent | Create/replace; empty path → inbox; returns resolved path + hash |
| `patch_note` | Patch section | destructive | Edit one section: `operation` `replace` (default, the content under the heading), `append`, `prepend`, `insert_before` or `insert_after`; `section` is the heading text or a heading path (`Project A > Notes`); a name matching several headings is an error listing their paths and lines, nothing is written; returns `hash`, `startLine` and `endLine` of the new content |
| `replace_in_note` | Replace text in note | destructive | Replace exact `old_text` with `new_text` (empty deletes); must occur once unless `replace_all`; searched over the whole file, frontmatter included; returns `hash`, `replacements` and `excerpt` (the edited lines with two lines around them, numbered as in `read_note`) |
| `append_to_note` | Append to note | additive | Append markdown to the end |
| `prepend_to_note` | Prepend to note | additive | Insert at top of body (after frontmatter) |
| `delete_note` | Delete note | destructive, idempotent | Delete a note |
| `move_note` | Move note | — | Move/rename; backlinks follow; refuses existing targets |
| `search_notes` | Search vault | read-only | Ranked (BM25F), diacritics- and typo-insensitive, matches Czech word forms (`faktury` finds `fakturu`); when no note has every word, the notes missing one come back with `relaxed: true`; tag AND-filter, dir scope, regex; every hit has a `score` from 0 to 1 and `min_score` drops weak ones; `max_results` (default 20) and `offset` to page, `total`, `truncated` and a `hint` when cut; `response_format: concise` for path, title, score and the line number of the first match only; `context_lines`, `max_snippets` (see [search.md](search.md)) |
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
| `suggest_location` | Suggest location | Rank folders for new content by tag overlap and by words shared with the folder description; each candidate carries its folder's `description` |
| `suggest_tags` | Suggest tags | Excerpt, current tags, vault vocabulary, linked-note tags |
| `suggest_links` | Suggest links | Unreciprocated backlinks, title mentions, shared tags |
| `find_untagged` | Find untagged notes | Notes without tags |
| `find_orphans` | Find orphan notes | Notes with no links either direction |
| `find_inbox_stale` | Find stale inbox notes | Inbox notes untouched ≥ N days (default 14) |
| `find_undescribed_folders` | Find undescribed folders | Folders with at least `min_notes` notes of their own (default 5) and no description, most notes first, with up to five sample titles, the five most used tags and the `path` of the description note to write; at most 20, `total` and `truncated` |

The `find_*` reports leave folder description notes out: they are meant
to have no tags or links.

### Share tools (`VELLUM_SHARING=on`, default off)

Creating a link publishes a note to anyone holding the URL, so these are
opt-in separately from sharing itself: `VELLUM_SHARING=ui` serves links
but keeps minting them with the human. Neither tool touches note content.

| Tool | Title | Summary |
| --- | --- | --- |
| `share_note` | Share note | Create (or return) a note's public link. Optional `expires_in` as `24h`, `7d`, `4w`; omit for no expiry. Returns `url`, `token`, `expires_at`, `reused` |
| `unshare_note` | Stop sharing note | Revoke the link. Revoking an unshared note is a no-op, not an error |

Re-sharing a note returns the link it already has, so a URL already
handed out stays valid. `move_note` carries a link to the note's new
path and `delete_note` revokes it — whichever side the change came from.
Details: [sharing.md](sharing.md).

### Structured output

Every tool returns `structuredContent` validated against an
auto-generated output schema (from the Go result types), plus a
`TextContent` block for clients that predate structured output. For
most tools that block is the same JSON serialized. The tools whose
results are mostly note text put a plain rendering there instead, with
no JSON envelope and no escaping:

- `read_note`: `path:`, `hash:`, `tags:` and `lines:` header lines, a
  blank line, then the raw file.
- `read_notes`: per note a `==> path <==` line, then `hash:` and `tags:`
  lines, a blank line and the raw file (or the outline, an `error:` or a
  `skipped:` line); a summary line at the end.
- `search_notes`: a header (`39 notes`, or `notes 1-20 of 39` for a
  page), a `path (title)` line per note and its best matching line as
  `7: text`, with the file line number, and the hint last when the page
  was cut. The context windows and further snippets are in
  `structuredContent` only, and so is each hit's `score`. The concise
  format renders one `path:line (title)` line per note, without the
  matching line.
- `suggest_tags`: tag lists as comma-separated lines, then the excerpt.
- `suggest_links`: one `path (title): reason` line per candidate.

`read_note` returns `content` (the raw file, which `hash` is computed
over) but not `body`: the REST API keeps `body` for the web UI. The
measurements and the clients this was checked on are in
[payload-baseline.md](payload-baseline.md).

### Folder descriptions

A folder can say what it is for. The description is an ordinary note in
the folder, `_index.md` (or the name in `VELLUM_CONTEXT_FILE`, for vaults
that use `README.md` or `index.md`), so it stays visible and editable in
Obsidian or any editor and moves with its folder:

```markdown
---
description: Development of vellum. Specs, decisions, release notes.
---
```

The text is the `description:` frontmatter field, or else the first
paragraph of the note, at most 300 characters. `_index.md` in the vault
root describes the whole vault (at most 500 characters).

There is no tool for writing descriptions: an agent creates or updates
the note with `write_note` (read it first, then pass `expected_hash`),
as one sentence in the instructions tells it. A bare `_index.md` path is
the one file name `write_note` keeps in the vault root instead of the
inbox. Descriptions are kept in memory only, one per described folder,
and follow writes at once; a description edited in another editor shows
after the index is rebuilt.

Where they appear:

- `search_notes`: `contexts`, a map folder → description for the folders
  of the page's hits and their parent folders, once per response (not
  per hit). The text block ends with a `folder dir: description` line
  per folder.
- `list_notes`: `description` on each entry of `dirs`.
- `read_note` with `outline: true`: `contexts` with the folder chain from
  the top folder down to the note's, and the same `folder` lines in the
  text block.
- `suggest_location`: `description` on each candidate.
- The server instructions: the vault root's description, read when the
  server starts (a change shows after a restart).

The vault root's description is not repeated in `contexts`: it is in the
instructions once per session. A vault without description notes
answers exactly as before, without `contexts` or `description` keys.

### What changed recently

`list_notes` answers "what was worked on lately" without a tool of its
own: two optional parameters, which combine with `dir`, `recursive`,
`limit` and `offset`.

| Parameter | Values | Meaning |
| --- | --- | --- |
| `sort` | `path` (default), `modified` | `modified` lists the newest change first; notes changed at the same moment are ordered by path, so paging is stable. Each entry then carries `excerpt`, the note's first line of text from the index, so the agent need not open every note |
| `since` | `24h`, `7d`, `2w`, or a date such as `2026-11-01` | Only notes modified at or after then. A number of hours, days or weeks back from now, a date (midnight in the server's time zone) or an RFC 3339 time. Anything else is an error that lists these forms |

```
list_notes(recursive: true, sort: "modified", since: "7d", limit: 20)
```

With `since`, `total` counts the notes that pass the filter; `dirs`
still shows every subfolder with all its notes. Without either
parameter the listing is exactly what it was before.

The modification time is the file's own (`modTime`), read when the
folder is listed, so a note edited in another editor moves to the top
at once; its `excerpt` follows when the index re-reads the note. A
limitation to know: `git clone`, `git pull`, a backup restore or a
sync tool may set file times to when they ran, not to when the note was
last edited, and the whole set of touched notes then looks recently
changed.

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
- Notes changed outside vellum (another editor, `git pull`, a second
  vellum process) notify subscribers too, once the index's lazy rescan
  has seen them: at the first index-reading call after `VELLUM_RESCAN`
  (default `5s`) has passed. There is no file watcher; see
  [search.md](search.md#notes-changed-outside-vellum).
- Notifications are per-session and in-memory: they don't survive a
  server restart, and there is no replay (`Last-Event-ID` is not
  supported). Re-subscribe after a reconnect.

## Client setup

```sh
# Claude Code, remote server (OAuth)
claude mcp add --transport http vellum https://vellum.example.com/mcp

# Claude Code, local vault over stdio (no server, no auth; --scope user: every project)
claude mcp add --scope user vellum -- vellum mcp ~/notes

# Claude Code, local HTTP mode (`vellum -vault ~/notes` running; one shared index)
claude mcp add --scope user --transport http vellum http://localhost:8080/mcp

# Claude Code, stdio through Docker (nothing installed; --user lets it write as you)
claude mcp add --scope user vellum -- docker run -i --rm --user "$(id -u):$(id -g)" \
  -v "$HOME/notes:/vault" ghcr.io/freema/vellum -mcp-stdio

# claude.ai custom connector: URL https://vellum.example.com/mcp,
# client ID `vellum`, your VELLUM_CLIENT_SECRET.

# MCP Inspector against a local dev server
npx -y @modelcontextprotocol/inspector
```

In the Inspector the annotations show up per tool, the Resources tab
lists the vault, and subscribing to a note then editing it in the web UI
demonstrates the live `resources/updated` flow.

Claude Desktop and Cursor take the same JSON block (Claude Desktop:
*Settings → Developer → Edit Config*; Cursor: `~/.cursor/mcp.json`).
Give the full path of the binary, since apps started from the Dock or
Start menu do not see your shell's PATH; vellum expands the `~/` itself.
Claude Desktop can instead open `vellum.mcpb` from a release.

```json
{
  "mcpServers": {
    "vellum": {
      "command": "/path/to/vellum",
      "args": ["mcp", "~/notes"]
    }
  }
}
```
