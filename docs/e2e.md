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
