---
tags: [dev, engineering]
---
# Deploy checklist for search

## Decisions

- Latency went up after the last deploy, p99 is now around 3 ms.
- Code review needs one approval, two for changes in the payment path.
- Rate limiting lives in the gateway, not in each service.

## Status

The {svc} service logs JSON to stdout, the collector ships it to the log store. Alerts page the on-call engineer only for user-facing symptoms. The CI pipeline runs unit tests, linters and a container build in about six minutes.

## Next steps

- Integration tests are flaky when the queue is slow to start, add a readiness check.
- Secrets come from the vault at startup, never from environment files in the repo.
- Error budget for the quarter is almost spent, slow down risky changes.

## Notes

Feature flags let us deploy code that is not switched on yet. The health endpoint must not call other services, only check local state. Load test: the {svc} service handles 20 requests per second on two replicas.

## Context

Dependencies are updated every two weeks by a bot, a human merges the pull request.
