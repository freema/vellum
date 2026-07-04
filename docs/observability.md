# Observability — Sentry error reporting

vellum can report panics and internal (5xx) errors to [Sentry](https://sentry.io)
using the official [`getsentry/sentry-go`](https://github.com/getsentry/sentry-go)
SDK. It is **off by default** and **fully env-configurable** — vellum is open
source, so every deployment sets its **own** DSN. No DSN is ever committed.

## Enable it

Set the DSN in your environment (`.env`):

```bash
SENTRY_DSN=https://<key>@<org>.ingest.<region>.sentry.io/<project>
SENTRY_ENVIRONMENT=production   # optional, tags events (default: production)
```

With `SENTRY_DSN` empty, Sentry is disabled and all capture calls are no-ops.
The `release` is set automatically to the running vellum version.

## What gets reported

- **Panics** anywhere in the HTTP layer (`/mcp`, `/api/*`, OAuth) are recovered,
  returned to the client as a `500`, sent to Sentry, **and** recorded as an
  error event in the **Activity** panel of the workspace UI — so when something
  breaks with the MCP you can see it in the UI, not only in the dashboard.
- **Internal server errors** (`500`) from the REST API.

Normal, expected outcomes (a 404 for a missing note, a 409 write conflict, an
invalid OAuth request) are **not** reported — they are not failures.

## Privacy

Consistent with [docs/logging.md](logging.md): tokens, the client secret and
note contents are never sent to Sentry. Only the error/panic value, the request
method and path, and the layer tag are attached.

## Quick verify

Point a client at the server and trigger any handled request; healthy traffic
produces no events. To confirm the pipeline end-to-end, enable a DSN in a
throwaway Sentry project, cause a deliberate 500 in a dev build, and check the
issue appears in Sentry and as an error row in the workspace Activity panel.
