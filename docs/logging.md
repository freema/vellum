# Logging policy

vellum logs to stderr via `log/slog`. The policy is short because the rule
is absolute:

## Never logged

- `VELLUM_CLIENT_SECRET` or any comparison against it
- access tokens, refresh tokens, authorization codes (not even prefixes)
- `Authorization` headers
- note contents or note titles (vault data stays out of logs)

## Logged

- startup configuration that is not secret: port, vault path, client id,
  issuer URL, index size and build time
- lifecycle events: shutdown signal, server errors
- auth *outcomes* may be added later (e.g. "token rejected") — always
  without the token value

## Rules for contributors

1. New log lines with request data need a review pass against this list.
2. Errors bubbling out of the auth package must not embed token material.
   (`ErrInvalidToken` is a static string — keep it that way.)
3. When in doubt, log the *event*, not the *value*.
