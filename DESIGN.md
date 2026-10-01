# vellum — design (the source of truth for the UI)

The final design was made in Claude design, project **"Vellum design brief"**
(maintainer access only):
https://claude.ai/design/p/91a9aa89-6f50-4f5a-8eb4-fb29c5e2246c

Local copies in this repository are **the reference for implementing the SPA**:

| File | Contents |
|---|---|
| `design/Vellum-Design-System.dc.html` | 10 sections: colors, typography, buttons, inputs, tags & status, tree + note list, command palette, markdown preview, feedback (toast / modal / empty state / status bar), dark mode |
| `design/Vellum-Workspace.dc.html` | The main screen at 1440×900 — the three-panel workspace: tree, note list, editor with a markdown toolbar, move/rename/delete, status dropdown, command palette, help modal, onboarding tour, toasts |
| `design/Vellum-Auth.dc.html` | 1a connect/login card, 1b OAuth authorize (tool selection + scopes) |
| `design/Vellum-Error-Pages.dc.html` | 404, 410 and 500 states, reconnect overlay |
| `design/Vellum-Logo.dc.html` | Mark, wordmark, lockups, variants, favicons |
| `design/support.js` | The Claude design runtime — do not edit |

> `.dc.html` is the Claude design format (templates + runtime). It is **not
> drop-in code**: use it as the source of tokens, markup patterns and the exact
> look. Open a file in a browser to preview it.

## Implementation rule (binding)

**The SPA follows the design very closely — pixel-faithful.** Tokens (colors,
fonts, radii, shadows) are copied 1:1 into CSS custom properties
(`web/src/styles/tokens.css`). Components are built from the design system,
the workspace layout follows `Vellum-Workspace.dc.html`. Deviate from the
design only after agreeing on it, not as an "improvement" along the way.

## Design tokens

### Colors — light "paper"

| Token | Hex | Use |
|---|---|---|
| `bg` | `#FAF7F2` | app background, top bar, left panel |
| `bg-panel` | `#FFFFFF` | editor, cards, active list item |
| `bg-list` | `#FCFAF6` | middle panel (note list) |
| `bg-statusbar` | `#F3F1EC` | status bar |
| `bg-segmented` | `#F1EDE5` | segmented controls |
| `ink` | `#2A2622` | primary text |
| `ink-body` | `#3A342E` | text in the markdown preview |
| `ink-code` | `#4A443C` | raw markdown / code |
| `ink-muted` | `#7A7266` | secondary text |
| `ink-faint` | `#9A938A` | placeholders, meta |
| `line` | `#E8E2D8` | hairline borders |
| `line-input` | `#E0D9CD` | input and button borders |
| `line-soft` | `#F0EBE2` | dividers in lists |
| `accent` | `#8B6F47` (hover `#7A6039`) | links, primary button, active states |
| `accent-soft` | `#F1EAE0` | hover/selection, active tree item, tag chip background |
| `highlight` | bg `#E6D6B8`, text `#5E4A23` | search match highlight |
| `wikilink/quote` | `#D8C9AF` | wikilink underline, blockquote border |

### Status colors

| State | Color | Soft bg | Text |
|---|---|---|---|
| backlog | `#9A938A` | `#F3F1EC` | `#6E6A62` |
| in-progress | `#C08A3E` | `#F7EEDD` | `#976A28` |
| done | `#5F8D5F` | `#E9F0E7` | `#4A714A` |
| danger | `#B3543D` | `#F7ECE8` (border `#E5CCC4`) | `#8A3E29` |
| knowledge (note type) | `#C4BBA9` | — | — |

### Colors — dark "midnight ink"

`bg #16141F` · `bg-panel #1E1B29` · `line #2E2A3A` · `ink #E8E4DC` · `ink-muted #8F8A9B` · `accent #C2A878` · `accent-soft #2A2433`

### Typography

- **Newsreader** (opsz 6..72, 400/500/600 + italic) — headings, note titles, wordmark
- **Inter** (400/450/500/600) — UI text, 12–16 px
- **JetBrains Mono** (400/500) — tags, paths, frontmatter, code, labels (9.5–13 px)
- Line height 1.6–1.7 in content

The fonts are self-hosted with the SPA; no CDN fonts.

### Radii and shadows

- Radius: buttons/inputs **6 px**, chips 4–5 px, panels 8 px, modal/command palette 10 px, auth cards 12 px, logo tile 20 px
- Almost no shadows — hairline borders instead. Exceptions: command palette `0 12px 40px -12px rgba(42,38,34,.22)`, the modal similarly, the active list item `inset 3px 0 0 #8B6F47`

## Screens

**Workspace (1440×900):** top bar 52 px (wordmark, 340 px search with ⌘K, active tag chips, version, ⚙) · left tree 220 px (VAULT: Inbox with a badge, Projects expanded, Archive; a TAGS section) · middle list 326 px (breadcrumb header + ＋, All/Tasks/Knowledge toggle, All/Backlog/In progress/Done segmented control; items: dot (task) or square (knowledge) + serif title + snippet + chips + age; active = white background + a 3 px accent bar on the left) · right editor (30 px serif title, Edit/Split/Preview, meta row: status badge, tags, modified, collapsed frontmatter; split view raw mono | preview) · status bar 30 px (mono path, note count, Saved ✓).

**Auth:** 1a — connect card, 440 px (logo, "Connect to your vault", Client secret password input, Connect, mono `claude mcp add …` snippet). 1b — OAuth authorize card, 520 px (vellum ←→ Claude, the allowed tools with read/write/delete scope badges and checkboxes, a vault.read/write/delete scopes row, Deny / "Authorize N tools"). For a self-registered client, 1b also carries the client-secret field from 1a above the scopes row: approving requires the owner's secret.

**Logo:** a rounded tile in accent brown with the top-right corner folded over (parchment; the fold is darker, `#6F5836`) and a serif "v" in cream `#FBF7F0`; the wordmark "vellum" in lowercase Newsreader 500; variants on paper / panel / midnight (accent → `#C2A878`) / reversed; favicons at 16/32/56; tagline *"a calm window into a folder of markdown"*.

## What the design adds beyond the original spec

1. **Task vs knowledge note types** — a dot vs a square in the list, and an All/Tasks/Knowledge filter. Frontmatter `type: task|knowledge`.
2. **OAuth authorize with per-session selection of tools and scopes** (vault.read/write/delete). The card is implemented; per-tool selection is a possible follow-up — today every tool is listed and granted.
