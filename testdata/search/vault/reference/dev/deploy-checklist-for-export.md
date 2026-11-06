---
tags: [notes, ops]
---
# Deploy checklist for export

## Notes

- The CI pipeline runs unit tests, linters and a container build in about six minutes.
- Each deploy is tagged with the git commit, so tracing a regression is quick.
- Alice owns the {svc} service this quarter.
- Code review needs one approval, two for changes in the payment path.

## Context

The canary deploy gets five percent of traffic for fifteen minutes. Rate limiting lives in the gateway, not in each service. The incident started with a config change that was not reviewed. The health endpoint must not call other services, only check local state.

## Next steps

- Timeouts are set on every outgoing call, the default is two seconds.
- Latency went up after the last deploy, p99 is now around 800 ms.
- Every deploy goes through the staging environment first.
- The {svc} service logs JSON to stdout, the collector ships it to the log store.

## Status

Integration tests are flaky when the queue is slow to start, add a readiness check. Alerts page the on-call engineer only for user-facing symptoms. We deploy on Thursday mornings, never on Friday afternoon. Build artifacts are signed and kept for ninety days.

## Open questions

Secrets come from the vault at startup, never from environment files in the repo. Load test: the {svc} service handles 8 requests per second on two replicas.
