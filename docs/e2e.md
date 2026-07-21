# E2E scenarios (Chrome DevTools MCP)

Continuous testing discipline (PHY-129): the MCP layer is covered by Go
integration tests in CI (`internal/mcpserver/http_test.go` — real HTTP,
OAuth flow, every tool, traversal attempts, auth rejection). The UI is
driven by Claude through Chrome DevTools MCP against a local compose stack.

## Setup

```sh
task e2e            # fixture vault -> ./vault, builds and starts compose
```

The stack runs on http://localhost:8090 (override `VELLUM_PORT` in `.env`)
with the deterministic fixture from `testdata/vault/`: 7 notes — tags,
wikilinks, tasks in all three states, an untagged orphan, an ~11 KB note.
Auth is off by default locally; export `AUTH_ENABLED=true` +
`VELLUM_CLIENT_SECRET` before `task e2e` to include the OAuth scenarios.

## Checklist (extend with every feature)

Core:

- [ ] SPA loads at `/`, wordmark + fonts render (serif Newsreader)
- [ ] Tree shows Inbox (badge), Projects with `demo` child, Archive, TAGS cloud
- [ ] Selecting a folder narrows the list; breadcrumb follows
- [ ] Open `Roadmap`-style note: title serif 30px, meta row (type, status,
      tags, modified), split view raw|preview
- [ ] Edit in the textarea → autosave (status meta flips unsaved → modified)
- [ ] ⌘S saves immediately
- [ ] Create note via ＋ (lands in current folder / inbox), then delete it
      via MCP/REST and confirm it leaves the list
- [ ] ＋ opens the new note with its title selected; typing a title and
      blurring renames the file (`inbox/untitled.md` → `inbox/<slug>.md`,
      status bar + URL follow), while retitling an already-named note
      leaves its filename alone
- [ ] Write `inbox/untitled.md` via MCP, then ＋ in the UI without a
      re-scan → the new note takes `untitled-2.md` and the MCP note is
      untouched
- [ ] ＋, then type `# Heading` in the body without touching the title
      field and click away → the file is named after the heading
- [ ] Type into the body right after committing a title (inside the rename
      window) → the text lands in the renamed file, no `untitled.md` is left
      behind
- [ ] Rename a note to `.private` → refused with a toast; rename
      `notes.md` → `Notes.md` (case only) → succeeds
- [ ] ⌘K palette: type a word from one note (`SEARCH-NEEDLE-ALPHA`),
      snippet shows with the match highlighted, ↑↓ + ↵ opens
- [ ] Tag filter: click a tag in the TAGS cloud → list narrows; active tag
      chip appears in the top bar with ×
- [ ] Type toggle Tasks/Knowledge and status segmented control filter the
      list (fixture has one task per state)
- [ ] Set a task status via MCP `set_status` → list dot color updates
      after refresh
- [ ] Conflict: modify the open note on disk, then edit in the UI →
      "Note changed on disk" dialog; verify both buttons
- [ ] Deep link `/n/projects/demo/deep-dive.md` renders after reload
      (SPA fallback)

Persistence & sync (see [workspace.md](workspace.md)):

- [ ] Click a tag + folder + type/status filters → URL gains
      `?tags=…&dir=…&type=…&status=…`; hard reload keeps the whole view
- [ ] Open a note from the filtered list → `/n/…?tags=…` (query preserved);
      Back steps to the previous note, not through filter clicks
- [ ] Delete the selected folder via ⋯ → `dir` disappears from the URL,
      other params stay
- [ ] Toggle theme, reload → theme sticks (and no white flash in dark);
      switch editor to Preview, reload and switch notes → mode sticks
- [ ] Type into a note and reload within 1 s → the text survives
      (pagehide keepalive flush)
- [ ] Append to the open note via MCP, focus the window → content updates
      without a reload; delete it via MCP → 410 state; recreate → editor
      recovers

MCP resources & annotations (see [mcp.md](mcp.md)):

- [ ] Inspector: tools show titles + read-only/destructive annotations;
      `initialize` carries instructions, no `logging` capability
- [ ] Resources tab lists the vault notes; reading one returns raw
      markdown; a bogus URI answers `-32002`
- [ ] Subscribe to a note, edit it in the web UI → the SSE stream gets
      `notifications/resources/updated`; `list_changed` fires only on
      create/delete/title change, not on autosaves

OAuth (auth enabled):

- [ ] Fresh load shows the connect card (artboard 1a)
- [ ] Wrong secret → inline error; correct secret → workspace loads
- [ ] `/mcp` without a token → 401 with `WWW-Authenticate` challenge
- [ ] `claude mcp add --transport http vellum http://localhost:8090/mcp`
      completes the browser consent (artboard 1b) and tools work

Visual (per PHY-130/115 — pixel-faithful):

- [ ] Side-by-side against `design/Vellum-Workspace.dc.html`
- [ ] `/dev/components` against `design/Vellum-Design-System.dc.html`,
      light and dark (⚙ toggles the theme)

## After every release

Run the smoke subset on <your-server> — see `docs/deployment.md`.
