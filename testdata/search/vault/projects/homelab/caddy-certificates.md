---
tags: [homelab, tls]
---
# Certificates on the reverse proxy

Caddy terminates TLS for every service in the homelab and renews the certificates on its own through ACME.

## How renewal works

Caddy starts renewing about 30 days before expiry. Internal services use a wildcard certificate obtained with the DNS challenge, so nothing has to be reachable from outside.

## Rotate by hand

When the DNS API token changes or a certificate looks wrong:

1. Update the token in the environment file.
2. Remove the old certificate from the Caddy data directory.
3. Reload Caddy, it requests new certificates on the next request.

Check the expiry with `openssl s_client -connect home.example.com:443`.
