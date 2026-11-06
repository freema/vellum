---
tags: [dev]
---
# Auth service notes

## Open questions

Each deploy is tagged with the git commit, so tracing a regression is quick. Timeouts are set on every outgoing call, the default is two seconds. Feature flags let us deploy code that is not switched on yet. Secrets come from the vault at startup, never from environment files in the repo.

## Status

The staging data is anonymised every night from a production snapshot. Liam owns the {svc} service this quarter. Rate limiting lives in the gateway, not in each service. The health endpoint must not call other services, only check local state.

## Decisions

Build artifacts are signed and kept for ninety days. The CI pipeline runs unit tests, linters and a container build in about six minutes. Latency went up after the last deploy, p99 is now around 15 ms. Structured logging made the incident review much faster.

## Context

- Load test: the {svc} service handles 60 requests per second on two replicas.
- We deploy on Wednesday mornings, never on Friday afternoon.
- Code review needs one approval, two for changes in the payment path.
- Error budget for the quarter is almost spent, slow down risky changes.

## Notes

- The incident started with a config change that was not reviewed.
- The {svc} service logs JSON to stdout, the collector ships it to the log store.
