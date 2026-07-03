# vellum — design (zdroj pravdy pro implementaci)

Finální design vygenerovaný v Claude designu, projekt **„Vellum design brief"**:
https://claude.ai/design/p/91a9aa89-6f50-4f5a-8eb4-fb29c5e2246c

Lokální kopie v repu (**tohle je reference pro implementaci SPA**):

| Soubor | Obsah |
|---|---|
| `design/Vellum-Design-System.dc.html` | 10 sekcí: barvy, typografie, tlačítka, inputy, tagy & status, strom + note list, command palette, markdown preview, feedback (toast/modal/empty state/status bar), dark mode |
| `design/Vellum-Workspace.dc.html` | Hlavní obrazovka 1440×900 — třípanelový workspace |
| `design/Vellum-Auth.dc.html` | 1a connect/login karta, 1b OAuth authorize (výběr nástrojů + scopes) |
| `design/Vellum-Logo.dc.html` | Značka, wordmark, lockupy, varianty, favicony |
| `design/support.js` | Runtime Claude designu — needitovat |

> `.dc.html` je formát Claude designu (šablony + runtime), **není to drop-in kód** — slouží jako zdroj tokenů, markup vzorů a přesného vizuálu. Otevři v prohlížeči pro náhled.

## Pravidlo implementace (závazné)

**SPA se drží designu HODNĚ blízko — pixel-faithful.** Tokens (barvy, fonty, radius, stíny) se přebírají 1:1 do CSS custom properties / Tailwind configu. Komponenty se stavějí podle design systemu, layout workspace podle `Vellum-Workspace.dc.html`. Odchylky od designu jen po domluvě, ne „vylepšení" za pochodu. V Linearu to hlídá dedikovaná issue (SPA design system) a je to zapsané v PHY-115.

## Design tokens

### Barvy — light „paper"

| Token | Hex | Užití |
|---|---|---|
| `bg` | `#FAF7F2` | pozadí app, top bar, levý panel |
| `bg-panel` | `#FFFFFF` | editor, karty, aktivní list item |
| `bg-list` | `#FCFAF6` | prostřední panel (seznam poznámek) |
| `bg-statusbar` | `#F3F1EC` | status bar |
| `bg-segmented` | `#F1EDE5` | segmented přepínače |
| `ink` | `#2A2622` | primární text |
| `ink-body` | `#3A342E` | text v markdown preview |
| `ink-code` | `#4A443C` | raw markdown / kód |
| `ink-muted` | `#7A7266` | sekundární text |
| `ink-faint` | `#9A938A` | placeholdery, meta |
| `line` | `#E8E2D8` | hairline bordery |
| `line-input` | `#E0D9CD` | bordery inputů a tlačítek |
| `line-soft` | `#F0EBE2` | dělítka v seznamech |
| `accent` | `#8B6F47` (hover `#7A6039`) | odkazy, primární tlačítko, aktivní stavy |
| `accent-soft` | `#F1EAE0` | hover/selection, aktivní strom, tag chip bg |
| `highlight` | bg `#E6D6B8`, text `#5E4A23` | zvýraznění shody v search |
| `wikilink/quote` | `#D8C9AF` | podtržení wikilinku, blockquote border |

### Stavové barvy

| Stav | Barva | Soft bg | Text |
|---|---|---|---|
| backlog | `#9A938A` | `#F3F1EC` | `#6E6A62` |
| in-progress | `#C08A3E` | `#F7EEDD` | `#976A28` |
| done | `#5F8D5F` | `#E9F0E7` | `#4A714A` |
| danger | `#B3543D` | `#F7ECE8` (border `#E5CCC4`) | `#8A3E29` |
| knowledge (typ poznámky) | `#C4BBA9` | — | — |

### Barvy — dark „midnight ink"

`bg #16141F` · `bg-panel #1E1B29` · `line #2E2A3A` · `ink #E8E4DC` · `ink-muted #8F8A9B` · `accent #C2A878` · `accent-soft #2A2433`

### Typografie (Google Fonts)

- **Newsreader** (opsz 6..72, 400/500/600 + italic) — nadpisy, titulky poznámek, wordmark
- **Inter** (400/450/500/600) — UI text 12–16 px
- **JetBrains Mono** (400/500) — tagy, cesty, frontmatter, kód, labely (9.5–13 px)
- Line-height 1.6–1.7 v obsahu

### Radius & stíny

- Radius: tlačítka/inputy **6 px**, chips 4–5 px, panely 8 px, modal/command palette 10 px, auth karty 12 px, logo dlaždice 20 px
- Stíny téměř žádné — hairline bordery. Výjimky: command palette `0 12px 40px -12px rgba(42,38,34,.22)`, modal podobně, aktivní list item `inset 3px 0 0 #8B6F47`

## Obrazovky

**Workspace (1440×900):** top bar 52 px (wordmark, search 340 px s ⌘K, aktivní tag chips, verze, ⚙) · levý strom 220 px (VAULT: Inbox s badge, Projects rozbalené, Archive; sekce TAGS) · prostřední seznam 326 px (breadcrumb hlavičky + ＋, toggle All/Tasks/Knowledge, segmented All/Backlog/In progress/Done, položky: tečka(task)/čtvereček(knowledge) + serif titulek + snippet + chips + stáří; aktivní = bílé bg + 3px accent pruh vlevo) · pravý editor (serif titulek 30 px, Edit/Split/Preview, meta řádek: status badge, tagy, modified, sbalený frontmatter; split raw-mono | preview) · status bar 30 px (mono cesta, počet poznámek, Saved ✓).

**Auth:** 1a — connect karta 440 px (logo, „Connect to your vault", password input Client secret, Connect, mono snippet `claude mcp add …`). 1b — OAuth authorize karta 520 px (vellum ←→ Claude, výběr povolených nástrojů se scope badges read/write/delete + checkboxy, řádek scopes vault.read/write/delete, Deny / „Authorize N tools").

**Logo:** zaoblená dlaždice v accent hnědé s přehnutým pravým horním rohem (pergamen; fold tmavší `#6F5836`) + serifové „v" v krémové `#FBF7F0`; wordmark „vellum" lowercase Newsreader 500; varianty on paper/panel/midnight (accent → `#C2A878`)/reversed; favicony 16/32/56; tagline *„a calm window into a folder of markdown"*.

## Co design zavádí nad rámec původní spec

1. **Typ poznámky task vs knowledge** — tečka vs čtvereček v seznamu, filtr All/Tasks/Knowledge. Frontmatter `type: task|knowledge` → promítnuto do PHY-110 a PHY-115.
2. **OAuth authorize s per-session výběrem nástrojů a scopes** (vault.read/write/delete) → promítnuto do PHY-112 (vizuál karty = must, výběr nástrojů = nice-to-have).
