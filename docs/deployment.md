# Deployment (self-hosted, behind a reverse proxy)

vellum runs on a small VPS behind a reverse proxy (this example uses Caddy).
Public URL — substitute your own domain:

> **https://vellum.example.com** (point a subdomain at the box; set the Caddy
> site + `VELLUM_ISSUER_URL` to match)

## Layout

- **Server:** `/root/vellum/` — `docker-compose.yml`, `.env`, `vault/` (bind mount),
  `Caddyfile.vellum` (site block, merged into `/root/openclaw/Caddyfile`).
- **Local control:** `<your-local-deploy-dir>/Taskfile.yml`
  — `deploy`, `pull`, `start`, `stop`, `restart`, `update`, `status`, `logs`,
  `health`, `tunnel` (debug fallback).
- vellum has **no public port**; it sits on the shared docker network
  (`openclaw_openclaw-network`) and only Caddy (ports 80/443, UFW-open) reaches it.
- Image updates are **manual and deliberate**: `task update` after a release.
  No watchtower — we always know which version runs (pin it in `.env` via
  `VELLUM_IMAGE`).

## Reverse proxy gotchas (learned the hard way in openclaw-mcp)

- **`VELLUM_ISSUER_URL` must be the public HTTPS URL.** Without it the
  OAuth metadata advertises `http://localhost:<port>` and every client
  fails after the authorize step.
- **`TRUST_PROXY=1` behind Caddy/Traefik/nginx.** Rate limiting keys on
  the client IP; behind a proxy that means reading `X-Forwarded-For`,
  which is only safe (and only done) when this flag is set.
- **The connector URL must end with `/mcp`.** The bare domain is not the
  MCP endpoint — connecting Claude to `https://host/` instead of
  `https://host/mcp` yields a 404 after OAuth.

## Release → server cycle

1. Tag `v*` → GitHub Actions builds the multi-arch image → GHCR.
2. Bump `VELLUM_IMAGE` in the local `.env` (or keep `:latest`).
3. `task update` (deploy + pull + restart).
4. Run the smoke test checklist below.

## Smoke test checklist (after every release)

Run from `<your-local-deploy-dir>/`:

- [ ] `task update` completes without errors
- [ ] `task health` — public `/healthz` returns `{"status":"ok","version":"<expected tag>"}`
      and the container reports `healthy`
- [ ] TLS certificate is valid (no curl `-k` needed)

From M3 on (MCP + OAuth):

- [ ] `claude mcp add --transport http vellum https://vellum.example.com/mcp`
- [ ] OAuth flow completes with the client secret
- [ ] `list_notes` returns the seeded vault notes
- [ ] `write_note` + `read_note` round-trip works
- [ ] Unauthorized request (no/bad token) is rejected

From M4 on (UI):

- [ ] SPA loads in the browser at the public URL
- [ ] Optionally: claude.ai custom connector connects (URL must end with `/mcp`)

## Seeded test vault

`/root/vellum/vault/` contains `inbox/welcome.md` (knowledge),
`projects/vellum/vellum-deploy.md` (task, in-progress) and
`projects/vellum/smoke-test.md` (task, backlog) — enough to exercise
list/read/search and task filters. Owned by uid 65532 (distroless nonroot).
