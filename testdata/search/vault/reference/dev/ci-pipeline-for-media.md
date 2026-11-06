---
tags: [dev, notes]
---
# CI pipeline for media

## Next steps

- Build artifacts are signed and kept for ninety days.
- Each deploy is tagged with the git commit, so tracing a regression is quick.
- Rate limiting lives in the gateway, not in each service.

## Notes

- We deploy on Tuesday mornings, never on Friday afternoon.
- Error budget for the quarter is almost spent, slow down risky changes.
- Secrets come from the vault at startup, never from environment files in the repo.

## Status

The CI pipeline runs unit tests, linters and a container build in about six minutes. The canary deploy gets five percent of traffic for fifteen minutes. Structured logging made the incident review much faster.

## Open questions

- Integration tests are flaky when the queue is slow to start, add a readiness check.
- The {svc} service logs JSON to stdout, the collector ships it to the log store.
- Frank owns the {svc} service this quarter.
