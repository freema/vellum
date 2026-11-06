# Changelog

All notable changes to **vellum** are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security
- Web UI: `react-router` 7.18.1 → 7.18.5
  ([GHSA-qwww-vcr4-c8h2](https://github.com/advisories/GHSA-qwww-vcr4-c8h2),
  a CSRF bypass in React Router's RSC mode). The UI is a client-side SPA
  and does not use that mode, so vellum was not affected; the update
  clears the advisory. The build tooling (`vite` 8.1.3 → 8.3.6 with its
  `postcss`, `nanoid` and `source-map-js`) is updated too. It only runs
  when the UI is built and is not part of the binary.

## [1.13.0] — 2026-11-06

Notes you can hand to somebody. vellum also runs locally next to your
editor, agents read only the part they need, and search ranks with BM25F.

### Security
- **Approving a connection requires the vault's client secret**
  ([GHSA-75v9-hg2f-5g9v](https://github.com/freema/vellum/security/advisories/GHSA-75v9-hg2f-5g9v)).
  Also released as 1.12.2 — see there for the details. If you ran
  1.13.0-beta.1 reachable from the internet with `VELLUM_SHARING=on`,
  also look through **Public links** for links you did not create: an
  agent token can publish notes, and links outlive a restart.
- **Revoke in the Connections panel cuts the client off.** It used to only
  hide the session: the client's token kept working, and its next call put
  the session back. Revoke now drops every access and refresh token of
  that client, so it has to be approved on the consent screen (with the
  client secret) again. A connection that uses the client secret itself,
  or any connection while auth is off, shows "Can't revoke" instead, since
  revoking would not stop it; rotate `VELLUM_CLIENT_SECRET` for those.
- `golang.org/x/text` 0.37.0 → 0.39.0 (GO-2026-5970, an infinite loop on
  invalid input).

### Added
- **Public share links.** Any note can become a read-only URL that opens
  without an account — for handing over a document your agent just wrote,
  without exporting it or giving anyone access to the vault. Turn it on
  with `VELLUM_SHARING=ui` (only you can publish) or `=on` (agents get
  `share_note`/`unshare_note` over MCP too); the default stays `off`,
  since this is the only part of vellum that answers an unauthenticated
  request.

  A link is a pointer, not a copy: the reader always sees the current
  note, an edit shows up immediately, and revoking takes effect on the
  next request. Re-sharing a note returns the link it already has, so a
  URL handed out yesterday keeps working. The link follows the note when
  it is renamed or moved — the same promise `move_note` makes for
  backlinks — and dies with it when the note or its folder is deleted,
  whether the change came through the web UI or MCP.

  The reader page is one document and nothing else: no tree, no search,
  no editor, and `[[wikilinks]]` render without navigating, because they
  point into a vault the reader cannot see. Only the note's body is
  served, so frontmatter never leaves the vault. `/s/{token}/raw` hands
  over the markdown file itself.

  Links live in `<vault>/.vellum/shares.json`, the first vellum state
  that is deliberately persistent: a URL given to another person must not
  depend on the server's uptime, the way the in-memory OAuth tokens do.
  It travels with a vault backup, and note writes cannot reach it — the
  vault refuses dot-segment paths, so an agent cannot publish a note by
  writing a file. Optional expiry from 1 hour to 365 days.

  In the workspace: **Share** in the note header (create, copy, set an
  expiry, watch the view count, stop sharing), a mark on shared notes in
  the list, and a **Public links** drawer listing every live link with a
  Revoke button. Creating and revoking land in the activity feed.

  Reference: [docs/sharing.md](docs/sharing.md); the security posture is
  in [SECURITY.md](SECURITY.md) and
  [docs/threat-model.md](docs/threat-model.md).
- **Run vellum locally with one command.** `vellum mcp <vault>` serves MCP
  over stdio for a local folder, for example
  `claude mcp add vellum -- vellum mcp ~/notes`. `~/` is expanded by vellum,
  so paths in MCP client JSON configs work too. `-vault <path>` sets the
  folder in both modes (the argument or `-vault` first, then
  `VELLUM_VAULT_PATH`, then `./vault` in HTTP mode only), and `-init` lets
  stdio mode create a missing folder. The startup log shows the absolute
  vault path and the number of notes. `vellum -mcp-stdio` with
  `VELLUM_VAULT_PATH` keeps working.
- **Ready-to-run binaries on every release.** Each release now attaches
  archives with the web UI built in, for Linux, macOS and Windows (amd64
  and arm64), plus `checksums.txt` and signed build provenance:
  `gh attestation verify vellum_darwin_arm64.tar.gz --repo freema/vellum`
  shows the archive came from this repository's release workflow. The
  archive names carry no version, so
  `releases/latest/download/vellum_<os>_<arch>.tar.gz` always points at
  the newest stable release, which also makes
  `mise use -g github:freema/vellum` work. Pre-release tags stay
  pre-releases and leave the Docker `latest` tag alone.
- **Homebrew cask.** Stable releases can publish a cask to the
  freema/homebrew-tap tap (`brew install freema/tap/vellum`, then
  `brew upgrade vellum`); pre-releases never touch it. The cask clears the
  macOS quarantine flag after install, since the binary is not notarized.
  Without the tap token a release simply skips this step.
- **Claude Desktop extension.** Every release ships `vellum.mcpb`. Open it
  in Claude Desktop on macOS (Apple Silicon and Intel) or Windows, pick
  the folder with your markdown notes, and vellum runs as a local MCP
  server. No terminal, Docker or config file needed.
- **MCP Registry entry.** vellum is prepared for the official MCP Registry
  as `io.github.freema/vellum`, listed with both the Docker image and the
  Claude Desktop bundle.
- **Notes changed outside vellum show up without a restart.** An edit in
  Obsidian or VS Code, a `git pull`, or a second vellum process over the
  same folder now reaches search, tags, backlinks, tasks, folder
  descriptions and the curator tools. Before a call that reads the index,
  vellum checks the disk once the rescan interval has passed
  (`VELLUM_RESCAN`, default `5s`, `off` disables). There is no file watcher
  and no background work, so it also works on Docker bind mounts and
  network drives; a rescan of 5 000 unchanged notes takes about 11 ms.
  Subscribers of `vellum://note/{path}` get `resources/updated` for these
  changes too, and a note renamed outside vellum keeps its share link when
  the move is unambiguous (same file name and size).
- **`read_note` reads part of a note.** `outline: true` returns just the
  headings with their line numbers (plus frontmatter, tags, hash and line
  count), `section` returns one heading and everything under it (exactly
  what `patch_note` replaces), and `start_line` / `max_lines` return a line
  range; `line_numbers: true` prefixes each line with its number. Lines
  count from 1 over the whole file, frontmatter included, the same
  numbering `search_notes` reports, so a search hit's line goes straight
  into `start_line`. The returned `hash` is always that of the whole note,
  so `expected_hash` still works after a partial read. Reading one section
  of a 620-line note costs about 6 KB instead of 64 KB. Calls without the
  new parameters return what they did before.
- **`read_notes` reads up to 20 notes in one call**, by a list of `paths` or
  a glob `pattern` such as `projects/x/*.md`. Every note comes back on its
  own (content, hash, tags and title, or its own error), so one bad path
  does not fail the rest. A byte budget keeps answers small: a note over
  `max_bytes_per_note` (default 10 KB) is skipped with its size and line
  count, never cut in the middle, and once `max_total_bytes` (default
  60 KB) is spent the remaining notes are skipped too. `outline_only: true`
  returns each note's outline instead. It never reads notes in hidden
  folders.
- **`replace_in_note` for small edits.** It replaces an exact piece of text
  (`old_text`, matched character for character, also in the frontmatter)
  with `new_text`. The text must occur once unless `replace_all` is set;
  otherwise the error lists the line of every match and nothing is
  written. The answer has the new hash, the number of replacements and the
  edited lines with two lines around them, numbered, so the agent can check
  the edit without reading the note again. An empty `new_text` deletes the
  text; `expected_hash` works as in the other edit tools.
- **`patch_note` can add to a section.** An optional `operation`: `replace`
  (the default, unchanged), `append` (end of the section), `prepend` (right
  under its heading), `insert_before` (before its heading) and
  `insert_after` (after the section's end). A heading next to inserted
  content always gets exactly one blank line. `section` also takes a
  heading path such as `Project A > Notes`, and the answer carries
  `startLine` and `endLine`, the lines the new content occupies.
- **Paging for `search_notes` and `list_notes`.** Both report `total`,
  `truncated` and a `hint` that says how to narrow the call or where the
  next page (`offset`) starts. `search_notes` gets
  `response_format: "concise"` (per note only the path, the title and the
  line number of the first match: about 2 kB for 20 hits),
  `context_lines` (`0` for the matching line alone) and `max_snippets`.
  `list_notes` gets `limit` and `offset`, and always returns `dirs`, the
  direct subfolders with their note counts, so listing the root shows the
  vault's structure in a few hundred bytes.
- **`list_notes` shows what changed recently.** `sort: "modified"` lists
  notes newest first, each with a short excerpt, and `since` keeps only
  notes modified in the last `24h`, `7d`, `2w` or since a date such as
  `2026-11-01`. Both combine with `dir`, `recursive`, `limit` and
  `offset`. The time is the file's own: after a `git clone`, a backup
  restore or a sync tool it may be the time of that operation.
- **Folder descriptions.** A folder can say what it is for in an
  `_index.md` note (frontmatter `description:`, or its first paragraph, up
  to 300 characters). `search_notes` returns the descriptions of the
  result folders once per answer in `contexts`, `list_notes` shows them on
  `dirs`, the `read_note` outline lists the folder chain, and
  `suggest_location` also matches words of the description, so a folder
  without shared tags can still be suggested. `_index.md` in the vault root
  describes the whole vault and is added to the server instructions (read
  at startup). Agents write descriptions with `write_note`; a bare
  `_index.md` path now stays in the vault root instead of going to the
  inbox. `VELLUM_CONTEXT_FILE` (default `_index.md`) suits vaults that use
  `README.md` or `index.md`.
- **Curator tool `find_undescribed_folders`** (with `VELLUM_CURATOR=on`):
  folders with at least five notes and no description, with sample titles
  and common tags.
- **Relevance score.** Every ranked hit of `search_notes` and
  `GET /api/search` carries a `score` from 0 to 1 that does not depend on
  the other hits, and `search_notes` takes an optional `min_score`
  (`0.3` keeps the clear matches) to drop weak hits before paging. Regex
  searches and pure tag filters are not ranked and carry no score.
- **Search understands Czech word forms.** `faktury` finds a note that says
  `fakturu`, and `smlouvami` finds `smlouvu`. A word-form match scores
  below an exact match and above a typo.
- **Relaxed search when no note holds every word.** Search then returns the
  notes that miss exactly one word, if the words they do hold carry most of
  the query's weight. These results are marked `relaxed: true` in
  `search_notes` and `GET /api/search`.
- For maintainers: `task bump VERSION=x.y.z` sets the release version in
  `server.json`, `mcpb/manifest.json`, the Claude plugin manifests and the
  image tags pinned in the README examples in one go;
  `task release-snapshot` builds every release archive into `dist/`
  without publishing; `task payload` replays typical agent
  scenarios and prints the bytes each MCP tool returns (baseline in
  [docs/payload-baseline.md](docs/payload-baseline.md)); a golden set of
  47 Czech and English queries gates search quality in CI
  ([docs/search-quality.md](docs/search-quality.md)).

### Changed
- **The task digest e-mail is redesigned.** It now comes as HTML in
  vellum's paper look, with a plain-text part for clients that prefer it.
  Every task links straight to the note in the workspace and shows its
  folder and when it was last updated, most recent first. A long section
  lists ten tasks and links to the rest, and done tasks updated since the
  last digest get a short *Done* section. The subject says what is open
  (`Vellum digest · 2 in progress, 18 in backlog`) and is now properly
  encoded, and the message carries `Date` and `Message-ID` headers, which
  spam filters expect.
- **A plain binary now listens on `127.0.0.1` only.** The HTTP server binds
  `VELLUM_HOST` (default `127.0.0.1`), so `vellum` started on a laptop is
  reachable from that machine alone. Set `VELLUM_HOST=0.0.0.0` to accept
  connections from the network, and enable `AUTH_ENABLED` when you do. The
  official Docker image sets `VELLUM_HOST=0.0.0.0`, so containers and
  compose files need no change; a bare-metal install reached from another
  host (a reverse proxy on a different machine, for example) must set it.
- **The auth-disabled warning names the listen address** and appears only
  when the server is reachable from the network. On loopback an info line
  says `/mcp` is reachable from this machine only.
- **Stdio mode no longer creates a missing vault or falls back to
  `./vault`.** It exits with an error that names the path; add `-init` to
  create the folder.
- **Search ranks with BM25F.** Rare words count more than common ones, a
  match in the title, file name, headings or tags counts more than one in
  the body, and a short note about the topic beats a long note that
  mentions it in passing. On the golden query set MRR rose from 0.79 to
  0.90 and nDCG@10 from 0.82 to 0.91; p95 latency on 5 000 notes stays
  within 10 % of before. The set of results only grows (word forms and
  relaxed matches); the order changed. For the transition,
  `VELLUM_RANKER=legacy` restores the previous ranking.
- **`search_notes` returns at most 20 notes per call by default** (was 50).
  Ask for more with `max_results`, or page with `offset`. The REST API and
  the web UI still return 50. **`list_notes` returns at most 200 notes per
  call** (`limit` and `offset` to page).
- **`read_note` over MCP no longer returns `body`.** Use `content` (the raw
  file, which `hash` is computed over); the REST API still returns `body`.
  Reading a note now sends about half as many bytes.
- **Tool text blocks are plain text.** The text block of `read_note`,
  `search_notes`, `suggest_tags` and `suggest_links` is no longer a JSON
  copy of the structured result: `read_note` sends a short header (path,
  hash, tags, line count) and the raw note, and `search_notes` one entry
  per hit with its path, title and best matching line with its number.
  The structured output is unchanged apart from the dropped `body`; other
  tools keep the JSON copy. Together with the changes above, the measured
  agent scenarios now send about 13 % of the bytes v1.12.1 did.
- **`patch_note` no longer silently edits the first matching heading.** If
  the section name matches several headings, it returns an error listing
  each candidate with its heading path and line number, and writes
  nothing; a heading path such as `Project A > Notes` picks one.
  `read_note` with `section` behaves the same way. `patch_note` is no
  longer marked idempotent, because appends and inserts add again when
  repeated.
- **Server instructions guide lean use.** They now tell agents to ask for
  a long note's outline first or use the line number from search, rank the
  edit tools (`replace_in_note` for a word or sentence, `patch_note` for a
  section, `append_to_note` / `prepend_to_note` to add text, `write_note`
  only for a rewrite), point at `list_notes` with `sort: "modified"` and
  `since` to see what is new, and explain `_index.md` folder descriptions.
- **Curator reports skip folder description notes**: `find_untagged`,
  `find_orphans` and `find_inbox_stale` no longer list `_index.md` notes.
- The Docker image carries the `io.modelcontextprotocol.server.name` label
  the MCP Registry needs, and `server.json` passes the registry's rules.
- The release workflow stops a stable tag whose manifests do not match it,
  before any image is built, and names the file that is out of date.

### Fixed
- The MCP registry entry (`server.json`) and the Claude Code plugin
  manifests carried version 1.9.0 for three releases; they now match the
  latest release.
- An edit that kept a note's size within the same second could be served
  stale from the search cache; the cache now keys on the nanosecond
  modification time.
- **Search uses far less memory.** The search cache holds each note in
  about a third of the memory it took, and the metadata index no longer
  keeps every note's full text alive by accident. On 5,000 realistic notes
  the server's heap after the first search drops from 250 MiB to 68 MiB.
  Results, scores and snippets are unchanged.
- **Large vaults no longer fall off a cliff.** Past 8,192 notes the search
  cache used to empty itself on every search, so each search read the whole
  vault from disk again (0.85 s on 15,000 notes). The cache now also
  accepts notes up to 256 MiB, keeps what it holds when full and reads only
  the rest from disk: 15,000 notes search in about 10 ms, 30,000 in about
  0.45 s instead of 1.7 s.

## [1.12.2] — 2026-10-01

### Security
- **Approving a connection requires the vault's client secret**
  ([GHSA-75v9-hg2f-5g9v](https://github.com/freema/vellum/security/advisories/GHSA-75v9-hg2f-5g9v),
  critical). Since 1.3.0 an MCP client could register itself and the
  consent screen issued a code to whoever pressed *Authorize*; a
  self-registered client then exchanged it without a secret. Anyone who
  could reach a server with `AUTH_ENABLED=true` could therefore mint a
  token with full read, write and delete access to the vault. For a
  self-registered client the consent screen now asks for
  `VELLUM_CLIENT_SECRET` and issues no code without it. The configured
  `vellum` client, which presents the secret at `/token`, keeps its
  one-click consent. The page can no longer be framed by another site.

  Upgrading restarts the server, which drops every token issued so far.
  If your server was reachable from the internet, also look through the
  vault for changes you did not make.
- `golang.org/x/text` 0.37.0 → 0.39.0 (GO-2026-5970, an infinite loop on
  invalid input). The image is rebuilt with the current Go 1.26 toolchain,
  which carries the standard library fixes released since 1.12.1.

## [1.12.1] — 2026-07-22

### Fixed
- **A symlink can no longer smuggle a note into a hidden directory.** 1.12.0
  refused a dot-prefixed path lexically, but `visible/n.md` where `visible`
  links to `.private/` still landed the note under a dot segment the vault
  scan skips. Note writes, renames and folder creation now resolve symlinks
  before deciding a target is hidden.

## [1.12.0] — 2026-07-21

Notes captured in the web UI get real filenames.

### Added
- **Create-only note writes.** `PUT /api/notes/{path}` honors
  `If-None-Match: *`: an existing note answers **412** instead of being
  replaced. `If-Match` keeps precedence, and the two conditions together are
  refused with 400 rather than silently resolved one way.

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
- **Leaving a note flushes its draft.** The last keystrokes inside the 1 s
  autosave window used to ride on a timer that fired after the editor had
  already moved on — and were lost outright when the note had changed path
  in the meantime.

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

[Unreleased]: https://github.com/freema/vellum/compare/v1.12.1...HEAD
[1.12.1]: https://github.com/freema/vellum/compare/v1.12.0...v1.12.1
[1.12.0]: https://github.com/freema/vellum/compare/v1.11.0...v1.12.0
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
