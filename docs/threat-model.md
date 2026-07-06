# Threat model

## Trust boundaries

```
agent (Claude) ──HTTPS──▶ reverse proxy ──▶ vellum ──▶ vault directory
   untrusted                TLS boundary      auth       filesystem
                                              boundary   boundary
```

1. **Agent → vellum (`/mcp`)**: untrusted until proven otherwise. Every
   request needs a valid Bearer token issued by vellum's own OAuth flow.
   Browser calls additionally pass an Origin allowlist. Loopback origins
   (`localhost` / `127.0.0.1` / `[::1]`) are exempt from the allowlist so
   local tools like the MCP Inspector work out of the box — that exemption
   relaxes only CORS, never authentication: a loopback Origin can only
   originate from software already running on the client's machine, tokens
   are still required, and no `Allow-Credentials` header is ever sent.
2. **vellum → vault**: the vault layer treats every path as hostile.
   `filepath.Clean` + root-prefix check, symlink rejection (file symlinks
   outright, directory symlinks may not escape the root), null-byte
   rejection, extension allowlist, 10 MB size cap. Covered by unit tests
   including traversal and symlink escape cases.
3. **vellum → anything else**: does not exist. vellum makes no outbound
   connections, executes no code, and calls no LLM. AI curation tools
   (PHY-113) only *prepare context* — the agent on the user's side decides.

## What a compromised/malicious agent can do

With a valid token, the agent can read, create, modify, move and delete
**markdown files inside the vault directory** — that is the entire blast
radius. It cannot:

- touch files outside `VELLUM_VAULT_PATH` (traversal protections),
- execute anything (no shell, no eval, distroless image without a shell),
- reach other services through vellum (no outbound calls),
- escalate in the container (non-root, read-only rootfs, no-new-privileges).

Mitigation for data loss stays operational: back up the vault (plain files).

## What theft of the client secret means

The secret is the single access key. Whoever holds it can complete the
OAuth flow and gets full vault access. Rotate it by changing
`VELLUM_CLIENT_SECRET` and restarting — all outstanding tokens die with
the process on restart anyway (in-memory storage).

## Known accepted risks (v1)

- **Single secret = single role.** No per-user identity or scoping until
  the team mode (vellum.yaml, PHY-119). The consent screen's tool list is
  informational; scopes are advertised but not enforced per-tool.
- **Tokens don't survive restarts.** Deliberate: no persistence, clients
  silently re-authorize.
- **The vault volume is only as safe as the host.** vellum adds no
  encryption at rest; use disk encryption if that matters.
