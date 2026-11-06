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
- **A plain binary listens on `127.0.0.1` only.** A proxy on the same
  machine reaches it as is. When the proxy runs elsewhere, set
  `VELLUM_HOST=0.0.0.0` (and `AUTH_ENABLED=true`). The Docker image
  already sets `VELLUM_HOST=0.0.0.0`, so containers need nothing.
- **The connector URL must end with `/mcp`.** The bare domain is not the
  MCP endpoint — connecting Claude to `https://host/` instead of
  `https://host/mcp` yields a 404 after OAuth.

## CORS: browser origins

Browser requests to `/mcp` and `/api` pass an Origin allowlist
(`VELLUM_ALLOWED_ORIGINS`, default claude.ai + claude.com) and only
allowlisted origins get CORS response headers (`CORS_ORIGINS`, defaults to
the same list).

**Loopback origins are always allowed** — `http(s)://localhost`,
`127.0.0.1` or `[::1]` on any port. That's what lets the
[MCP Inspector](https://github.com/modelcontextprotocol/inspector)
(`npx @modelcontextprotocol/inspector`, UI on `localhost:6274`) connect
straight to a deployed server without touching its env. This is safe by
construction: a loopback Origin can only be sent by a page already running
on the *client's* machine (the browser sets the header; a remote site
cannot forge it), no `Access-Control-Allow-Credentials` is ever emitted,
and every request still needs a valid Bearer token — CORS relaxation never
bypasses auth. See the [threat model](threat-model.md).

For any other origin (say, a web app you host yourself), add it explicitly:

```sh
VELLUM_ALLOWED_ORIGINS=https://claude.ai,https://claude.com,https://myapp.example.com
```

## Release → server cycle

1. Tag `v*` → GitHub Actions builds the multi-arch image → GHCR. Once the
   image is pushed, GoReleaser attaches the binaries (linux, macOS, Windows;
   amd64 and arm64, web UI embedded) with `checksums.txt` and signed build
   provenance to the GitHub Release. A tag with a hyphen (`v1.13.0-beta.1`)
   becomes a pre-release and leaves `:latest` alone. Before tagging, build
   the same archives locally with `task release-snapshot` (output in `dist/`,
   nothing is published).
   A stable tag also pushes `Casks/vellum.rb` to the `freema/homebrew-tap`
   repo, so `brew upgrade` picks it up; a pre-release leaves the tap alone.
   The push needs the `TAP_TOKEN` Actions secret (a fine-grained token with
   Contents read and write on that repo only). Without the secret the
   release still succeeds and simply skips the tap. The snapshot writes the
   cask to `dist/homebrew/Casks/vellum.rb` for a look before tagging.
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
