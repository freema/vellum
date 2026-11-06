---
tags: [ops, engineering]
---
# CI pipeline for gateway

## Decisions

- The CI pipeline runs unit tests, linters and a container build in about six minutes.
- We keep a changelog per service, written for the support team.
- Feature flags let us deploy code that is not switched on yet.
- Alerts page the on-call engineer only for user-facing symptoms.
- Structured logging made the incident review much faster.
- The {svc} service logs JSON to stdout, the collector ships it to the log store.
- Secrets come from the vault at startup, never from environment files in the repo.
- The incident started with a config change that was not reviewed.

## Next steps

Integration tests are flaky when the queue is slow to start, add a readiness check. Dependencies are updated every two weeks by a bot, a human merges the pull request. Hugo owns the {svc} service this quarter. Kate suggested splitting the {svc} service into a reader and a writer. Error budget for the quarter is almost spent, slow down risky changes. Load test: the {svc} service handles 20 requests per second on two replicas. Code review needs one approval, two for changes in the payment path. Latency went up after the last deploy, p99 is now around 15 ms.

## Open questions

We deploy on Friday mornings, never on Friday afternoon. The staging data is anonymised every night from a production snapshot. Each deploy is tagged with the git commit, so tracing a regression is quick. Every deploy goes through the staging environment first. The canary deploy gets five percent of traffic for fifteen minutes. The health endpoint must not call other services, only check local state. Timeouts are set on every outgoing call, the default is two seconds. Build artifacts are signed and kept for ninety days.

## Notes

Rate limiting lives in the gateway, not in each service. Feature flags let us deploy code that is not switched on yet. Kate suggested splitting the {svc} service into a reader and a writer. The CI pipeline runs unit tests, linters and a container build in about six minutes. The health endpoint must not call other services, only check local state. Error budget for the quarter is almost spent, slow down risky changes. Secrets come from the vault at startup, never from environment files in the repo. Integration tests are flaky when the queue is slow to start, add a readiness check.
