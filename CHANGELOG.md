# Changelog

All notable changes to **vellum** are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/freema/vellum/compare/v1.2.0...HEAD
[1.2.0]: https://github.com/freema/vellum/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/freema/vellum/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/freema/vellum/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/freema/vellum/compare/v0.2.0...v1.0.0
[0.2.0]: https://github.com/freema/vellum/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/freema/vellum/compare/v0.0.1...v0.1.0
[0.0.1]: https://github.com/freema/vellum/releases/tag/v0.0.1
