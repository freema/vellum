---
tags: [notes, ops]
---
# Logging in the export service

## Context

- Load test: the {svc} service handles 3000 requests per second on two replicas.
- The incident started with a config change that was not reviewed.
- The {svc} service logs JSON to stdout, the collector ships it to the log store.
- Latency went up after the last deploy, p99 is now around 60 ms.
- The health endpoint must not call other services, only check local state.
- Timeouts are set on every outgoing call, the default is two seconds.
- Dependencies are updated every two weeks by a bot, a human merges the pull request.

## Next steps

Code review needs one approval, two for changes in the payment path. Each deploy is tagged with the git commit, so tracing a regression is quick. Build artifacts are signed and kept for ninety days. Secrets come from the vault at startup, never from environment files in the repo. Structured logging made the incident review much faster. Frank suggested splitting the {svc} service into a reader and a writer. We keep a changelog per service, written for the support team.

## Open questions

- We deploy on Friday mornings, never on Friday afternoon.
- Integration tests are flaky when the queue is slow to start, add a readiness check.
- Kate owns the {svc} service this quarter.
- The staging data is anonymised every night from a production snapshot.
- Error budget for the quarter is almost spent, slow down risky changes.
- The CI pipeline runs unit tests, linters and a container build in about six minutes.
- Alerts page the on-call engineer only for user-facing symptoms.

## Status

Feature flags let us deploy code that is not switched on yet. The canary deploy gets five percent of traffic for fifteen minutes. Rate limiting lives in the gateway, not in each service. Every deploy goes through the staging environment first. Secrets come from the vault at startup, never from environment files in the repo. Dependencies are updated every two weeks by a bot, a human merges the pull request. The staging data is anonymised every night from a production snapshot.

## Decisions

- Structured logging made the incident review much faster.
- Integration tests are flaky when the queue is slow to start, add a readiness check.
