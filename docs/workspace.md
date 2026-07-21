# Web workspace reference

How the embedded SPA behaves: what lives in the URL, what persists
locally, how saving and vault synchronization work. Source:
`web/src/workspace/Workspace.tsx`, `web/src/lib/api.ts`.

## Routes

| Route | Meaning |
| --- | --- |
| `/` | Workspace with no note open |
| `/n/<vault path>` | Note open in the editor — deep-linkable and refresh-safe |
| `/wl/<target>` | Wikilink resolver: finds the target note, redirects to `/n/…` |
| `/dev/components` | Design-system gallery (development reference) |

## URL query parameters (shareable view state)

The list filters live in the query string, so a refresh keeps the view
and a filtered view can be bookmarked or shared:

| Param | Values | Effect |
| --- | --- | --- |
| `dir` | vault directory path | Tree selection; narrows the list to that folder |
| `tags` | comma-separated tag names | Tag AND-filter; active pills pin to the top of the tag cloud |
| `type` | `task` \| `knowledge` | Type toggle (absent = all) |
| `status` | `backlog` \| `in-progress` \| `done` | Status segmented control (absent = all) |

Example: `/n/projects/x/plan.md?dir=projects/x&tags=health,go&type=task`.

Semantics worth knowing:

- Filter changes **replace** the history entry — the Back button steps
  between notes, never through filter clicks.
- Opening, moving, renaming or deleting a note **preserves** the query
  string (`goto()` in `Workspace.tsx`).
- Deleting the selected folder removes its `dir` param in the same
  history operation that closes a note inside it.
- Unknown values are ignored (fall back to "all"), so a stale bookmark
  never breaks the view.

## localStorage (personal preferences)

| Key | Content | Notes |
| --- | --- | --- |
| `vellum_theme` | `"light"` / `"dark"` | First visit follows the OS `prefers-color-scheme`. An inline script in `index.html` applies it **before first paint** — no white flash for dark users. Keep that script's key in sync. |
| `vellum_editor_mode` | `"edit"` / `"split"` / `"preview"` | Global editor view mode. The editor panel remounts per note (`key={path}`), so this also keeps the mode stable when switching notes. |
| `vellum_projects_open` | `true`/`false` | Projects group expansion in the tree |
| `vellum_tags_expanded` | `true`/`false` | Tag cloud "Show all N" state |
| `vellum_onboarded` | `"1"` | Onboarding tour seen |

All access goes through `usePersistedState` (`web/src/lib/`), which
degrades to in-memory state in private mode and falls back to the
default on a corrupted value. The auth token is **sessionStorage**, not
localStorage — it dies with the tab, matching the server's in-memory
tokens.

## Creating and naming notes

- ＋ writes an empty `untitled.md` into the current folder (inbox when no
  folder is selected) and opens it with the title **selected**, so typing
  a title is the natural next keystroke.
- The create goes out as `If-None-Match: *`. The listing the free name is
  picked from can be a second stale — an agent may be writing to the same
  inbox — so the server answers **412** on a taken name and the UI steps to
  `untitled-2.md` rather than overwriting a note it never loaded.
- Committing the title of a note that is still `untitled*.md` renames the
  file to the slug of that title (`Weekly review` → `weekly-review.md`,
  uniquified with `-2` on collision), matching what `write_note` derives
  server-side. Typing the heading straight into the body does the same when
  you leave the editor pane. Notes that already carry a name of their own are
  never renamed automatically — `Move to… → Rename file` is the manual path.
- Any change of path (naming, rename, move, drag to a folder) remounts the
  editor on the new path, so the workspace carries an unsaved draft over
  before it navigates — otherwise the pending autosave would target the path
  the note just left.

## Saving and drafts

- Edits autosave with a **1 s debounce**; ⌘S saves immediately; title,
  status and checkbox changes commit without debounce.
- Saves send `If-Match` with the note's content hash. On a concurrent
  change the server answers 409 and the UI opens the conflict dialog
  (keep mine / take theirs) — nothing is clobbered silently.
- **Page-hide flush**: a refresh or tab close inside the debounce window
  would drop the last keystrokes, so `pagehide`/`visibilitychange`
  flushes the dirty draft with a `keepalive` fetch that outlives the
  page. Browsers cap keepalive bodies at ~64 KB — larger notes fall back
  to the normal flow (worst case: the last second of typing, same as
  before). The flush dedupes the two events so it never double-PUTs with
  a stale hash.

## Vault synchronization (MCP writes show up by themselves)

- The tree and list silently re-fetch every **30 s** and on window
  focus; a manual re-scan button exists for impatient moments.
- The open note revalidates every **20 s** and on focus (cheap
  `If-None-Match` 304 when unchanged). A remote change applies only when
  the local draft is clean — a dirty draft keeps the If-Match → conflict
  path.
- A note deleted behind the SPA's back switches the editor to the
  **HTTP 410 "This note was deleted"** state; if it is recreated, the
  next revalidation restores the editor.
- A nonsense or stale deep link renders the **404 "No note lives here"**
  state (with the query string intact); a failed read offers Retry
  (500). Design reference: `design/Vellum-Error-Pages.dc.html`.
- When the server goes away (deploy restart: network error or 502–504),
  a reconnect overlay polls `/healthz` with a countdown and restores the
  session when it returns.

## Keyboard

| Keys | Action |
| --- | --- |
| ⌘K | Command palette (search notes) |
| ⌘S | Save now |
| ⌘B / ⌘I | Bold / italic in the raw editor |
| Escape | Close palette, menus, overlays |
