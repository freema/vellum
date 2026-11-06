---
tags: [dev]
---
# Catalog service notes

## Status

We deploy on Tuesday mornings, never on Friday afternoon. The staging data is anonymised every night from a production snapshot. The incident started with a config change that was not reviewed. Timeouts are set on every outgoing call, the default is two seconds. Error budget for the quarter is almost spent, slow down risky changes.

## Context

Code review needs one approval, two for changes in the payment path. Load test: the {svc} service handles 120 requests per second on two replicas. Every deploy goes through the staging environment first. Rate limiting lives in the gateway, not in each service. Dependencies are updated every two weeks by a bot, a human merges the pull request.

## Decisions

Structured logging made the incident review much faster. The canary deploy gets five percent of traffic for fifteen minutes. Alerts page the on-call engineer only for user-facing symptoms. Grace owns the {svc} service this quarter. Latency went up after the last deploy, p99 is now around 400 ms.

## Notes

- Feature flags let us deploy code that is not switched on yet.
- The CI pipeline runs unit tests, linters and a container build in about six minutes.
- The health endpoint must not call other services, only check local state.
- Each deploy is tagged with the git commit, so tracing a regression is quick.
- Integration tests are flaky when the queue is slow to start, add a readiness check.

## Next steps

- The {svc} service logs JSON to stdout, the collector ships it to the log store.
- We keep a changelog per service, written for the support team.
