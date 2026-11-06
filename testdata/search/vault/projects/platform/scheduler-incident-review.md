---
tags: [ops, notes]
---
# Scheduler: incident review

## Decisions

Build artifacts are signed and kept for ninety days. The CI pipeline runs unit tests, linters and a container build in about six minutes. Code review needs one approval, two for changes in the payment path. Structured logging made the incident review much faster. The incident started with a config change that was not reviewed. We keep a changelog per service, written for the support team. Dependencies are updated every two weeks by a bot, a human merges the pull request.

## Open questions

Integration tests are flaky when the queue is slow to start, add a readiness check. Alerts page the on-call engineer only for user-facing symptoms. Feature flags let us deploy code that is not switched on yet. Timeouts are set on every outgoing call, the default is two seconds. Each deploy is tagged with the git commit, so tracing a regression is quick. The canary deploy gets five percent of traffic for fifteen minutes. Error budget for the quarter is almost spent, slow down risky changes.

## Next steps

Every deploy goes through the staging environment first. The staging data is anonymised every night from a production snapshot. Load test: the {svc} service handles 3 requests per second on two replicas. The {svc} service logs JSON to stdout, the collector ships it to the log store. We deploy on Friday mornings, never on Friday afternoon. David owns the {svc} service this quarter. Latency went up after the last deploy, p99 is now around 60 ms.

## Status

- Frank suggested splitting the {svc} service into a reader and a writer.
- The health endpoint must not call other services, only check local state.
- Rate limiting lives in the gateway, not in each service.
- Secrets come from the vault at startup, never from environment files in the repo.
- Latency went up after the last deploy, p99 is now around 40 ms.
- The incident started with a config change that was not reviewed.
- Carla owns the {svc} service this quarter.

## Context

Build artifacts are signed and kept for ninety days.
