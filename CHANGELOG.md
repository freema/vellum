# Changelog

All notable changes to **vellum** are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/freema/vellum/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/freema/vellum/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/freema/vellum/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/freema/vellum/compare/v0.2.0...v1.0.0
[0.2.0]: https://github.com/freema/vellum/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/freema/vellum/compare/v0.0.1...v0.1.0
[0.0.1]: https://github.com/freema/vellum/releases/tag/v0.0.1
