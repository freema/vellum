---
tags: [ops]
---
# Export service design

## Notes

Feature flags let us deploy code that is not switched on yet. Latency went up after the last deploy, p99 is now around 20 ms. Structured logging made the incident review much faster. The {svc} service logs JSON to stdout, the collector ships it to the log store. The CI pipeline runs unit tests, linters and a container build in about six minutes. Load test: the {svc} service handles 15 requests per second on two replicas. Every deploy goes through the staging environment first. We keep a changelog per service, written for the support team. Each deploy is tagged with the git commit, so tracing a regression is quick.

## Status

Timeouts are set on every outgoing call, the default is two seconds. The health endpoint must not call other services, only check local state. The staging data is anonymised every night from a production snapshot. Error budget for the quarter is almost spent, slow down risky changes. Rate limiting lives in the gateway, not in each service. Frank owns the {svc} service this quarter. Dependencies are updated every two weeks by a bot, a human merges the pull request. The canary deploy gets five percent of traffic for fifteen minutes. We deploy on Friday mornings, never on Friday afternoon.

## Decisions

Secrets come from the vault at startup, never from environment files in the repo. Code review needs one approval, two for changes in the payment path. Integration tests are flaky when the queue is slow to start, add a readiness check. The incident started with a config change that was not reviewed. Alerts page the on-call engineer only for user-facing symptoms. Build artifacts are signed and kept for ninety days. Alice suggested splitting the {svc} service into a reader and a writer. The health endpoint must not call other services, only check local state. Alerts page the on-call engineer only for user-facing symptoms.

## Next steps

Load test: the {svc} service handles 40 requests per second on two replicas. Integration tests are flaky when the queue is slow to start, add a readiness check. We keep a changelog per service, written for the support team. Rate limiting lives in the gateway, not in each service. Kate owns the {svc} service this quarter. Secrets come from the vault at startup, never from environment files in the repo. The staging data is anonymised every night from a production snapshot. Timeouts are set on every outgoing call, the default is two seconds. The {svc} service logs JSON to stdout, the collector ships it to the log store.

## Open questions

- The CI pipeline runs unit tests, linters and a container build in about six minutes.
- Load test: the {svc} service handles 12 requests per second on two replicas.
