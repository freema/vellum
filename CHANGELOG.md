# Changelog

All notable changes to **vellum** are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- **A note created in the web UI is now named after its title.** New notes
  still start as `untitled.md`, but naming one in the editor moves the file
  to the matching slug — `Weekly review` lands in `inbox/weekly-review.md`,
  the same filename `write_note` would have picked over MCP. Until now the
  title only ever reached the note's `# heading`, so every note captured
  from the workspace stayed `untitled.md`, `untitled-2.md`, … in the vault
  and only the MCP path produced readable filenames
  ([#1](https://github.com/freema/vellum/issues/1)). A note that already has
  a name of its own is never renamed behind your back; use *Move to… →
  Rename file* for those.
- **Creating a note can no longer overwrite one it did not know about.** The
  workspace picks the first free `untitled-N.md` from its listing, which can
  be a second out of date when an agent is writing to the same inbox; the
  create now goes out as a conditional request (`If-None-Match: *`) and steps
  to the next free name instead of replacing the existing note.
- **Keystrokes typed while a note is being moved are no longer lost.** The
  editor panel reloads when a note changes path (rename, *Move to…*, drag to
  a folder), and anything typed inside that window used to be autosaved to
  the path the note had just left — a write that fails, silently. The edits
  now travel with the note to its new path.
- **Retitling a note back to a title it already had is written again.** The
  editor compared the new title against the one the note carried when it was
  opened, so `Alpha → Beta → Alpha` left the file saying `Beta` while the
  header said `Alpha`.
- **A case-only rename works.** `notes.md` → `Notes.md` was refused as
  "already exists" on macOS and Windows, where the two names are the same
  file.
- **Notes can no longer be written to a hidden path.** `.private.md` (or any
  folder starting with a dot) was accepted by the rename box and by
  `write_note`, but the vault scan skips dotfiles — the note vanished from
  the list, search and MCP on the next index build while sitting on disk.
- **A very long title yields a filename the filesystem accepts.** Slugs are
  capped at 200 bytes on a character boundary, instead of failing the write
  with `ENAMETOOLONG`.
- **The editor no longer conflicts with its own save.** A second write
  started while one was in flight — blur landing on the heels of the
  autosave, ⌘S over a queued save — carried the pre-flight hash and raised a
  spurious "Note changed on disk" dialog. Saves are queued and always write
  against the newest known hash.
- **A rename that finishes after you have moved on stays where you are.** The
  navigation used the note that was open when the rename started, so a slow
  rename could yank the workspace back from the note you had just opened.

## [1.11.0] — 2026-07-11

### Added
- **Resource reads carry the content hash, modtime and size.** Reading a
  note through its resource URI (`vellum://note/{path}`) now returns
  `hash`, `modTime` and `size` in `ResourceContents._meta`, mirroring what
  the `read_note` tool already reports. A note attached as context —
  through a resource picker or an @-mention — can therefore seed the
  `expected_hash` on a later write, so the resource path gets the same
  conflict-safe footing as the tool path instead of being a snapshot with
  weaker freshness semantics. Since 1.9.0 resource reads returned only the
  markdown text, with no way to tie an attached note back to a safe write.

## [1.10.0] — 2026-07-08

The web vault stays signed in.

### Added
- **The web UI now keeps you signed in for up to 30 days.** It renews its
  1-hour access token silently in the background from the rotating refresh
  token and stores the session in `localStorage`, so a page reload, a closed
  tab or a browser restart no longer drops you back at the connect screen.
  Previously the web vault held only the 1-hour access token in
  `sessionStorage` and logged you out on the hour or when the tab closed —
  the 30-day refresh token shipped in 1.9.1 was never used by the web client.

### Changed
- **The web client refreshes without re-sending the client secret.** A valid,
  rotating refresh token is now sufficient for the `refresh_token` grant, so
  the embedded SPA — which keeps no secret after the initial login — renews on
  the refresh token alone, exactly as the public MCP clients already did.
  Minting the first token pair still requires the client secret. Existing web
  sessions re-authenticate once after upgrading, since the session moved from
  `sessionStorage` to `localStorage`.

## [1.9.1] — 2026-07-07

### Changed
- **Refresh tokens now last 30 days** (was 24 hours). A connected client
  re-authorizes far less often — only after 30 days of inactivity, since
  every refresh rotates the token and resets the clock. Access tokens stay
  short-lived at 1 hour.

## [1.9.0] — 2026-07-06

MCP resources & subscriptions, tool annotations, and a workspace that
remembers its view.

### Added
- **Notes are MCP resources.** Every note is listed as a resource
  (`vellum://note/{path}`, `text/markdown`) with a URI template for
  arbitrary paths, so MCP clients can attach notes as context without a
  tool call. Clients can **subscribe** to a note and receive
  `notifications/resources/updated` whenever it changes — through any door
  (MCP tools or the web editor). The resource list re-announces only on
  create/delete/title change, not on every keystroke autosave.
- **Tool annotations and titles.** All 21 tools now carry
  `readOnlyHint` / `destructiveHint` / `idempotentHint` /
  `openWorldHint: false` plus a human-readable title, so clients can
  auto-allow reads and warn only on genuinely destructive calls
  (`delete_note`, `write_note` overwrite, `patch_note`).
- **Server instructions.** The MCP handshake now includes a short guide to
  the vault conventions (inbox flow, `expected_hash` concurrency,
  wikilinks, task statuses, resource URIs) — agents no longer have to
  discover them by trial and error.
- **Filters live in the URL.** The selected folder, active tags and the
  type/status filters persist as query parameters
  (`?dir=…&tags=a,b&type=task&status=done`): a refresh keeps the view and
  a filtered view can be bookmarked or shared. Opening notes keeps the
  filters; Back still steps between notes, not filter clicks.
- **UI preferences survive a refresh.** Theme (initialised from the OS
  `prefers-color-scheme`, applied before first paint — no white flash),
  the editor view mode (edit/split/preview, now also stable across note
  switches), the Projects group expansion and the tag-list "Show all"
  state are stored in `localStorage`.

### Fixed
- **The last second of typing no longer dies with the tab.** Autosave is
  debounced at 1 s; a refresh or tab close inside that window used to drop
  the keystrokes. The dirty draft is now flushed with a `keepalive`
  request when the page hides.
- The MCP server no longer advertises the `logging` capability it never
  used.

### Docs
- New [docs/mcp.md](docs/mcp.md) — the full MCP reference (transports,
  handshake, every tool with annotations, resources, subscriptions,
  client setup) — and [docs/workspace.md](docs/workspace.md) — the web
  UI reference (URL scheme, storage keys, autosave/conflict/flush,
  self-sync). The e2e checklist gained persistence & resources
  scenarios.

## [1.8.0] — 2026-07-06

Synced from the updated Claude Design "Vellum Workspace" project.

### Added
- **Tag filter in the left panel.** A "Filter tags…" box narrows the tag
  list by name; tags are sorted by frequency (most used first) and each
  shows its note count. The list caps at 12 tags with a "Show all N"
  toggle (scrollable when expanded), and a "clear" link appears while a
  tag filter is active. Active tags stay pinned to the top.
- **Folder ⋯ menu.** Hovering a folder shows a ⋯ button opening a small
  menu with "New note here" and "Delete folder…" — replacing the inline ×
  that was too easy to hit by accident. Deleting is now a deliberate
  two-step action; the confirm dialog stays.

### Fixed
- **A nonsense note URL no longer presents as a server error.** Reading a
  path that can never be a note (wrong extension, invalid shape) returned
  400, which the workspace rendered as the 500 "The vault couldn't be read"
  state. `GET /api/notes/{path}` now answers 404 for such paths, so the UI
  shows "No note lives here". Writes keep the explicit 400.

## [1.7.0] — 2026-07-06

Designed error pages, a self-syncing workspace, and localhost CORS.

### Added
- **Designed error states** (design: `Vellum-Error-Pages.dc.html`). A note
  deep link that doesn't resolve shows "No note lives here" (404) with the
  path and search/back actions; a note deleted while open switches to "This
  note was deleted" (410); a failed read offers Retry (500). A missing
  static path now gets a styled 404 page instead of Go's plain text (browser
  requests only — API clients keep plain/JSON errors).
- **The workspace keeps itself in sync with the vault.** The tree and note
  list silently re-scan every 30 s and on tab focus, so notes created,
  changed or deleted by an MCP agent appear without the manual re-scan
  button. The open note revalidates by ETag (a cheap 304 when unchanged) and
  folds in remote changes as long as there are no unsaved local edits —
  dirty drafts keep the existing conflict flow.
- **"Vellum is restarting" reconnect overlay** — when the server goes away
  mid-session (deploy restart), the workspace shows the 503 state, polls
  `/healthz` with a countdown and reconnects on its own.

### Fixed
- **Note deep links no longer 404.** `/n/<path>.md` looked like a static
  asset to the SPA fallback (dot in the last segment) and returned a plain
  "404 page not found" instead of loading the app.
- **Czech characters no longer break in list excerpts** — the excerpt was
  cut at a fixed byte offset, which could split a multi-byte character
  (č, ř, ě …) and render `�` in the note list. It now cuts on a rune
  boundary.

### Changed
- **Loopback origins are always CORS-allowed** — the MCP Inspector (and any
  other tool served from `localhost` / `127.0.0.1` / `[::1]`) can now
  connect to a deployed vellum without editing `VELLUM_ALLOWED_ORIGINS`.
  Auth is unaffected: a Bearer token is still required, no
  `Allow-Credentials` is sent, and a loopback `Origin` can only come from
  software already running on the client's own machine
  ([docs/deployment.md](docs/deployment.md#cors-browser-origins)).

## [1.6.0] — 2026-07-04

Full-text search, second pass: typo-tolerant, diacritics-insensitive, and
another ~5× faster.

### Changed
- **Search tolerates typos** — terms of 4–7 letters may miss by one edit,
  8+ by two ("preklapy" finds "překlepy"). Typo hits always rank below exact
  hits, short terms stay strict, and the snippet highlights the real word
  from the note.
- **Search ignores diacritics** — "ukol" finds "úkol", "poznamka" finds
  "poznámka", in both directions (query and content are folded). Snippets
  still show the original accented text.
- **Word-boundary ranking** — a hit at the start of a word outranks one
  buried inside another word ("note" ranks *Note taking* above *Keynotes*).
- **~5× faster ranking pass** — search runs in two phases (a parallel,
  allocation-free scoring sweep, then snippet extraction for the top results
  only), the index hands the hot path shared entries instead of sorted
  copies, and the per-note word list is pre-computed for the typo matcher.
  Benchmarks on a 2 000-note vault (Apple M4 Pro): exact 0.38 ms,
  accent-folded multi-term 0.74 ms, worst-case typo 0.39 ms, no-match
  0.35 ms — all warm, single query. `-bench BenchmarkSearch` reproduces.
- The command palette highlights the matched text inside a result snippet.

## [1.5.0] — 2026-07-04

Performance release: loading and searching are dramatically faster.

### Changed
- **Gzip compression** for the API and the SPA — the app bundle now travels
  ~4× smaller and JSON listings shrink accordingly. (`/mcp` is untouched; its
  SSE stream must not be buffered.)
- **Proper HTTP caching** — hashed `/assets/` files are `immutable` for a
  year (no more re-downloading the bundle on every load), the HTML shell
  revalidates so deploys still appear immediately.
- **Search is ranked and RAM-fast.** Note content is cached in memory and
  invalidated precisely by the metadata index (modTime+size), so repeated
  searches never touch the disk. Results are ordered by relevance — title
  matches beat tag matches beat path matches beat body hits — instead of
  alphabetically. Multi-word queries AND their terms; the words in a row
  beat the words scattered apart; snippets pick the best-matching lines
  first. MCP and the REST API share one search cache.
- **Opening a note is instant when unchanged** — `GET /api/notes/{path}`
  honours `If-None-Match` (304), the SPA keeps a client-side note cache and
  revalidates by ETag, and hovering a note in the list prefetches it.
- `/api/search` takes a `limit` parameter; the command palette asks for 8
  instead of the default 50.
- The palette drops out-of-order search responses (a slow old query can no
  longer overwrite the results of a newer one).

## [1.4.0] — 2026-07-04

### Fixed
- **Login now survives a page refresh.** The session token is kept in
  sessionStorage (restored on load and verified), so reloading no longer drops
  you back to the connect screen.

### Added
- **Delete folders** — hover a folder in the tree for a `×`; a confirm dialog
  shows how many notes go with it, then it's removed recursively
  (`DELETE /api/folders/{path}`).
- **Re-scan vault** button in the tree header — pulls in notes added/changed
  via MCP without a full page reload.
- **Loading feedback** — a spinner while a note opens, a top progress bar when
  switching notes, and a "Searching…" indicator in the command palette.
- **MCP server icon** — the folded-leaf mark is advertised in the MCP server
  info (`icons` data URI + `websiteUrl`) and served at `/favicon.ico`, so the
  server shows an icon in the Inspector and connector lists.

## [1.3.0] — 2026-07-04

### Fixed
- **MCP clients can finally connect.** Implemented OAuth 2.0 Dynamic Client
  Registration (RFC 7591): a `POST /register` endpoint, `registration_endpoint`
  in the discovery metadata, and public (PKCE-only, no client secret)
  authorization-code / refresh flows. Previously vellum only accepted the fixed
  confidential client and demanded the shared secret on every grant, so the MCP
  Inspector, claude.ai, Cursor, etc. could not authenticate at all. Verified
  end-to-end via the MCP Inspector over HTTP (register → authorize → token →
  list/read/write tools).

### Added
- **Sentry error reporting** (`getsentry/sentry-go`), off unless `SENTRY_DSN`
  is set. Panics and internal 5xx are captured and also recorded as error
  events in the workspace **Activity** panel. See `docs/observability.md`.
- **Activity & errors** panel: an error-count badge on the activity button, an
  **Errors** filter, a search box, and expandable error rows (level, tool,
  status, message) with *Export → JSON* and *Fix with Claude Code*.
- **MCP Inspector** dev tasks: `task inspector` (stdio) and `task inspector-http`
  (Streamable HTTP), UI at `http://localhost:6274`.

### Changed
- `.env.example` documents the SMTP digest and Sentry settings.

## [1.2.1] — 2026-07-04

### Added
- **Redesigned connect / login screen** (design artboard 1a): a two-column card
  with sign-in on the left and a full "Connect a client" guide on the right —
  copyable endpoint, a four-step Claude.ai walkthrough, and tabs with ready-to-
  paste config for Claude Code, Claude Desktop, ChatGPT and Cursor.

### Changed
- The SMTP digest now also sends once shortly after start (to confirm the setup)
  in addition to the recurring interval.

## [1.2.0] — 2026-07-04

Synced from the updated Claude Design "Vellum Workspace" project.

### Added
- **MCP connections panel** — a live drawer of connected clients (name, kind,
  session id, uptime, last tool, call count) with a **Revoke** action, backed
  by in-memory session tracking on the server. A top-bar pill shows the active
  count.
- **Activity / curator log** — a right-hand drawer with a live timeline of what
  MCP clients and the curator did to the vault, an **All / Curator / MCP**
  filter, a curator status card and a **Run now** action.
- **Notifications** — a bell with an unread dot and a popover of derived
  notifications (untagged notes, stale inbox, open tasks, new sessions), with
  *mark all read* and dismiss.
- **Create folder** — a ＋ button in the tree opens an inline input; the folder
  is persisted (`POST /api/folders`) and nests under the selected folder.
- **GitHub Star button** in the top bar with a best-effort live star count.
- **SMTP digest** — an optional periodic e-mail of open tasks
  (`VELLUM_NOTIFY=on` + `SMTP_*`).
- **Guided tour** gains steps for the new folder, notifications, activity and
  star controls and is now fully keyboard-driven (**← / →**, Esc to skip).
- Help modal gains a **Connect a client** section (endpoint + CLI, both with
  copy buttons).
- New REST endpoints: `GET /api/connections` (+ `DELETE` to revoke),
  `GET /api/activity`, `GET /api/notifications`, `POST /api/curator/run`,
  `GET`/`POST /api/folders`.
- Icon set gains `bell`, `activity`, `sparkle` and a filled GitHub mark.

### Changed
- The list view hides the status segmented control for Knowledge notes.
- Tree folder badges show `matching / total` (e.g. `1/6`) while a tag filter is
  active; empty folders now appear in the tree.
- The MCP request path records per-tool activity and touches a session so the
  Connections and Activity panels reflect real usage.

## [1.1.0] — 2026-07-04

### Added
- **Markdown editor helper** — a formatting toolbar (bold, italic, H1/H2,
  bullet list, checkbox, quote, inline code, `[[wikilink]]`) that operates on
  the selection, `⌘B` / `⌘I` shortcuts, and a live word count.
- **Clickable checkboxes** in the preview toggle `- [ ]` ↔ `- [x]` and save.
- **Move / rename / delete** from the editor — a *Move to…* folder popover,
  a rename field, drag-and-drop of a note onto a tree folder, and a delete
  button with a confirmation dialog.
- **Task status dropdown** — change `backlog` / `in-progress` / `done`
  straight from the editor header.
- **Help modal** with a markdown cheatsheet, keyboard shortcuts and the
  note-type legend, plus a **guided onboarding tour** (shown once, remembered
  in `localStorage`).
- **Toasts** for move / delete / status feedback and a **Saved ✓ / Saving…**
  indicator in the status bar.
- **SVG icon set** replacing text glyphs across the workspace (fixes
  missing-glyph "tofu" boxes on some platforms).

### Changed
- Top bar splits theme, help and settings into separate controls; tree
  groups are collapsible.
- `design/Vellum-Workspace.dc.html` re-synced from the Claude Design project
  (adds the interactions above).

## [1.0.1] — 2026-07-04

### Fixed
- Connect screen key icon rendered as a missing-glyph box (`⚿`, U+26BF) on
  macOS — replaced with an inline SVG key.

### Added
- README author section, badges (release, CI, Go, license, GHCR) and GitHub
  topics for discoverability.

## [1.0.0] — 2026-07-04

First stable release.

### Added
- Self-hosted MCP server over a folder of markdown: 15 tools (+6 optional
  curator tools behind `VELLUM_CURATOR=on`).
- OAuth 2.1 (authorization code + PKCE, client credentials) with vellum as
  its own issuer; opaque in-memory tokens.
- Embedded React workspace UI served from the single Go binary.
- Launch documentation, version manifests, deployment + smoke-test checklist.

## [0.2.0] — 2026-07-04

### Added
- REST API (`/api/*`) with sha256 ETag / `If-Match` → `409` optimistic
  concurrency.
- Workspace SPA — three-pane tree / list / editor, command palette, filters.
- SPA embedded into the binary behind the `embedspa` build tag (`go build`
  stays node-free); ~24 MB image, ~5 MiB idle RAM.
- Fixture vault and end-to-end checklist (`task e2e`).

## [0.1.0] — 2026-07-03

### Added
- Vault layer: CRUD, atomic writes, path-traversal protection, in-memory
  metadata index (tags, backlinks, task states), parallel-scan search.
- Task frontmatter (`type: task | knowledge`, `status`), tags, curator
  suggestions.
- MCP tool surface over Streamable HTTP; OAuth model.

## [0.0.1] — 2026-07-03

### Added
- Walking skeleton: `/healthz`, distroless Docker image + compose, GitHub
  Actions CI and multi-arch release to GHCR, Taskfile.

[Unreleased]: https://github.com/freema/vellum/compare/v1.11.0...HEAD
[1.11.0]: https://github.com/freema/vellum/compare/v1.10.0...v1.11.0
[1.10.0]: https://github.com/freema/vellum/compare/v1.9.1...v1.10.0
[1.9.1]: https://github.com/freema/vellum/compare/v1.9.0...v1.9.1
[1.9.0]: https://github.com/freema/vellum/compare/v1.8.0...v1.9.0
[1.8.0]: https://github.com/freema/vellum/compare/v1.7.0...v1.8.0
[1.7.0]: https://github.com/freema/vellum/compare/v1.6.0...v1.7.0
[1.6.0]: https://github.com/freema/vellum/compare/v1.5.0...v1.6.0
[1.5.0]: https://github.com/freema/vellum/compare/v1.4.0...v1.5.0
[1.4.0]: https://github.com/freema/vellum/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/freema/vellum/compare/v1.2.1...v1.3.0
[1.2.1]: https://github.com/freema/vellum/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/freema/vellum/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/freema/vellum/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/freema/vellum/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/freema/vellum/compare/v0.2.0...v1.0.0
[0.2.0]: https://github.com/freema/vellum/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/freema/vellum/compare/v0.0.1...v0.1.0
[0.0.1]: https://github.com/freema/vellum/releases/tag/v0.0.1
