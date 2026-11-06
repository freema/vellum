---
tags: [engineering, ops]
---
# Catalog service design

## Open questions

Error budget for the quarter is almost spent, slow down risky changes. Latency went up after the last deploy, p99 is now around 40 ms. The incident started with a config change that was not reviewed. Frank owns the {svc} service this quarter. Build artifacts are signed and kept for ninety days.

## Decisions

Timeouts are set on every outgoing call, the default is two seconds. The {svc} service logs JSON to stdout, the collector ships it to the log store. Secrets come from the vault at startup, never from environment files in the repo. Structured logging made the incident review much faster. Code review needs one approval, two for changes in the payment path.

## Notes

We deploy on Monday mornings, never on Friday afternoon. The canary deploy gets five percent of traffic for fifteen minutes. Hugo suggested splitting the {svc} service into a reader and a writer. Integration tests are flaky when the queue is slow to start, add a readiness check. Every deploy goes through the staging environment first.

## Context

We keep a changelog per service, written for the support team. Alerts page the on-call engineer only for user-facing symptoms. Feature flags let us deploy code that is not switched on yet. The health endpoint must not call other services, only check local state. The staging data is anonymised every night from a production snapshot.

## Next steps

Load test: the {svc} service handles 120 requests per second on two replicas. Dependencies are updated every two weeks by a bot, a human merges the pull request.
