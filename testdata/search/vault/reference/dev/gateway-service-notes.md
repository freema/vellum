---
tags: [ops, engineering]
---
# Gateway service notes

## Next steps

Structured logging made the incident review much faster. Carla suggested splitting the {svc} service into a reader and a writer. Feature flags let us deploy code that is not switched on yet. Latency went up after the last deploy, p99 is now around 8 ms. Secrets come from the vault at startup, never from environment files in the repo.

## Notes

- Load test: the {svc} service handles 12 requests per second on two replicas.
- Build artifacts are signed and kept for ninety days.
- Error budget for the quarter is almost spent, slow down risky changes.
- Each deploy is tagged with the git commit, so tracing a regression is quick.
- Rate limiting lives in the gateway, not in each service.

## Status

The canary deploy gets five percent of traffic for fifteen minutes. The CI pipeline runs unit tests, linters and a container build in about six minutes. The staging data is anonymised every night from a production snapshot. We deploy on Monday mornings, never on Friday afternoon. Integration tests are flaky when the queue is slow to start, add a readiness check.

## Context

Dependencies are updated every two weeks by a bot, a human merges the pull request. Code review needs one approval, two for changes in the payment path. The health endpoint must not call other services, only check local state. Kate owns the {svc} service this quarter. Every deploy goes through the staging environment first.

## Open questions

The {svc} service logs JSON to stdout, the collector ships it to the log store.
