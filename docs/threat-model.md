# Threat model

## Trust boundaries

```
agent (Claude) ──HTTPS──▶ reverse proxy ──▶ vellum ──▶ vault directory
   untrusted                TLS boundary      auth       filesystem
                                              boundary   boundary
reader (a link) ─────────────▲
   untrusted, no token — reaches /s/{token} only, and only when
   VELLUM_SHARING is on
```

1. **Agent → vellum (`/mcp`)**: untrusted until proven otherwise. Every
   request needs a valid Bearer token issued by vellum's own OAuth flow.
   Browser calls additionally pass an Origin allowlist. Loopback origins
   (`localhost` / `127.0.0.1` / `[::1]`) are exempt from the allowlist so
   local tools like the MCP Inspector work out of the box — that exemption
   relaxes only CORS, never authentication: a loopback Origin can only
   originate from software already running on the client's machine, tokens
   are still required, and no `Allow-Credentials` header is ever sent.
2. **Reader → vellum (`/s/{token}`)**: exists only when
   `VELLUM_SHARING` is enabled — the single unauthenticated surface, and
   off by default. Three GET routes, serving the body of *one* note: the
   one its token names. See "Public share links" below.
3. **vellum → vault**: the vault layer treats every path as hostile.
   `filepath.Clean` + root-prefix check, symlink rejection (file symlinks
   outright, directory symlinks may not escape the root), null-byte
   rejection, extension allowlist, 10 MB size cap. Covered by unit tests
   including traversal and symlink escape cases.
4. **vellum → anything else**: does not exist. vellum makes no outbound
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

## Public share links (`VELLUM_SHARING`)

Off by default, and while off no route, tool or state file exists.

**What a share link grants.** Read access to the body of exactly one
note, to whoever holds a 192-bit token. Not the frontmatter, not the
note's path, not the folder it lives in, not any other note, and no way
to enumerate. The token is the whole capability, so treat the URL as the
secret it is; `Referrer-Policy: no-referrer` keeps it out of the Referer
of links inside the shared note, and `X-Robots-Tag: noindex` keeps it out
of search indexes.

**What theft of a link costs.** One note, readable until revoked. Revoke
it in the workspace (or with `unshare_note`) and the URL is dead on the
next request. Compare this with the client secret, which is the whole
vault: a leaked share link is deliberately the small failure.

**Why the link list is on disk.** `.vellum/shares.json` inside the vault.
It is the one piece of vellum state that must survive a restart — a URL
given to another person cannot depend on the server's uptime. Keeping it
in the vault also puts it inside the existing backup story, and out of
reach of note writes: the vault refuses paths with a dot segment, so an
agent with a valid MCP token cannot create, alter or read shares by
writing a note. Publishing is only ever an explicit call.

**Who may publish.** `VELLUM_SHARING=ui` restricts minting to the web
workspace; `=on` additionally registers `share_note`/`unshare_note` for
agents. The distinction matters because a prompt-injected agent that can
publish is a data-exfiltration path that does not require it to make an
outbound connection — which vellum otherwise cannot do. If the vault
holds anything you would not hand to a stranger by accident, `ui` is the
setting.

**Abuse of the endpoint.** Rate-limited to 120 requests/minute per client
IP (set `TRUST_PROXY=1` behind a proxy so that is a real address).
Unknown, malformed, expired and revoked tokens are indistinguishable
404s, so the endpoint cannot be used to confirm that a token ever
existed. Responses are `no-store`, `nosniff` and `DENY` framing.

**What it does not change.** vellum still makes no outbound connections —
a share is served, never pushed. There is still no identity: a link is
"anyone who has it", not "shared with a person".

## What theft of the client secret means

The secret is the single access key. Whoever holds it can complete the
OAuth flow and gets full vault access. Rotate it by changing
`VELLUM_CLIENT_SECRET` and restarting — all outstanding tokens die with
the process on restart anyway (in-memory storage).

## Known accepted risks (v1)

- **Single secret = single role.** No per-user identity or scoping until
  the team mode (vellum.yaml, PHY-119). The consent screen's tool list is
  informational; scopes are advertised but not enforced per-tool.
- **A share link is a bearer capability.** Anyone it reaches can read
  that note; there is no way to tell two readers apart, and no audit
  beyond a view counter. Accepted because the alternative — accounts for
  readers — is the thing vellum is deliberately not.
- **Tokens don't survive restarts.** Deliberate: no persistence, clients
  silently re-authorize.
- **The vault volume is only as safe as the host.** vellum adds no
  encryption at rest; use disk encryption if that matters.
