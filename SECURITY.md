# Security Policy

## What vellum is (and is not) allowed to do

vellum is a deliberately small attack surface:

- It operates on **one directory of markdown files** (`VELLUM_VAULT_PATH`) and
  nothing else. Every path is validated against traversal, symlink escapes
  and null bytes; only `.md`/`.markdown` files are touched.
- It **never executes code**, never spawns processes, and **never calls an
  LLM or any external service**. The only outbound activity is answering
  HTTP requests.
- There is **no database and no stored credentials** beyond the single
  client secret in the environment. Tokens are opaque, in-memory, and die
  with the process.

## Threat model (summary — details in docs/threat-model.md)

| Threat | Risk | Mitigation |
|--------|------|------------|
| Unauthorized vault access | High | OAuth 2.1 with client secret + PKCE; Bearer required on `/mcp` |
| Path traversal / escape | High | Path validation, symlink rejection, vault-root confinement (tested) |
| Token theft | Medium | 1h access tokens, refresh rotation, HTTPS-only deployment |
| Cross-origin abuse | Medium | Origin allowlist (403) + CORS allowlist |
| Denial of service | Low | Rate limiting on OAuth endpoints, 10 MB note size cap |
| Secret leakage in logs | Medium | Tokens/secrets are never logged (docs/logging.md) |

## Production checklist

- [ ] `AUTH_ENABLED=true` with a strong secret (`openssl rand -hex 32`)
- [ ] HTTPS via a reverse proxy (Caddy/Traefik); vellum itself never
      exposed directly
- [ ] `VELLUM_ISSUER_URL` set to the public URL, `TRUST_PROXY=1`
- [ ] Container runs read-only, non-root, `no-new-privileges`
      (the shipped docker-compose.yml does this)
- [ ] Pin the image version (`ghcr.io/freema/vellum:X.Y.Z`, not `:latest`)
- [ ] Back up the vault directory (it is plain files — rsync/git both work)

## Supported versions

Only the latest minor release receives security fixes. Pin a released tag and
upgrade when a patch is published.

| Version | Supported |
|---------|-----------|
| 1.2.x   | ✅ |
| < 1.2   | ❌ (upgrade) |

## Reporting a vulnerability

**Please report privately — do not open a public issue.**

- Preferred: open a private report via GitHub →
  [**Report a vulnerability**](https://github.com/freema/vellum/security/advisories/new)
  (repo **Security** tab → *Advisories* → *Report a vulnerability*). This keeps
  the discussion and any fix private until a coordinated release.
- Alternatively, email the maintainer (see the `freema` GitHub profile).

When reporting, please include: affected version, a description of the impact,
and the minimal steps to reproduce.

**What to expect**

- Acknowledgement within **3 working days**.
- An initial assessment and, if valid, a severity rating within **7 days**.
- A fix and coordinated disclosure as fast as the severity warrants; you will
  be credited in the advisory unless you prefer to stay anonymous.
