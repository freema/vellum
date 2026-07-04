# design/ — source of truth for the UI

Finished design exported from the Claude Design project
**"Vellum design brief"**:
<https://claude.ai/design/p/91a9aa89-6f50-4f5a-8eb4-fb29c5e2246c>

These `.dc.html` files are the **binding visual reference** for the SPA. They
are Claude-Design templates (markup + the `support.js` runtime), *not* drop-in
code — open them in a browser for a live preview. The SPA follows them closely
(pixel-faithful); deviations only by agreement. Design tokens are mirrored 1:1
into CSS custom properties — see [`../DESIGN.md`](../DESIGN.md).

| File | What it shows |
|------|---------------|
| `Vellum-Design-System.dc.html` | 10 sections: color, type, buttons, inputs, tags & status, tree + note list, command palette, markdown preview, feedback (toast/modal/empty/status bar), midnight-ink dark mode |
| `Vellum-Workspace.dc.html` | The main 1440×900 three-pane workspace — tree, note list, editor with markdown toolbar, move/rename/delete, status dropdown, command palette, help modal, onboarding tour, toasts |
| `Vellum-Auth.dc.html` | 1a connect / login card · 1b OAuth authorize with tool selection & scopes |
| `Vellum-Logo.dc.html` | Mark (folded leaf), wordmark, lockups, surfaces, favicons |
| `support.js` | Claude-Design runtime — **do not edit** |

## Keeping this folder in sync

The Claude Design project is upstream. To refresh a local copy, pull the file
from the project (via the DesignSync tooling / `/design-sync`) and overwrite
the matching `Vellum-*.dc.html` here. Record notable UI changes in
[`../CHANGELOG.md`](../CHANGELOG.md).
