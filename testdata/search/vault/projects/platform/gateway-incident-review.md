---
tags: [notes]
---
# Gateway: incident review

## Notes

The canary deploy gets five percent of traffic for fifteen minutes. Code review needs one approval, two for changes in the payment path. Every deploy goes through the staging environment first. Rate limiting lives in the gateway, not in each service. Load test: the {svc} service handles 400 requests per second on two replicas.

## Decisions

- The {svc} service logs JSON to stdout, the collector ships it to the log store.
- Secrets come from the vault at startup, never from environment files in the repo.
- We deploy on Tuesday mornings, never on Friday afternoon.
- Each deploy is tagged with the git commit, so tracing a regression is quick.
- Feature flags let us deploy code that is not switched on yet.

## Open questions

- Latency went up after the last deploy, p99 is now around 15 ms.
- Integration tests are flaky when the queue is slow to start, add a readiness check.
- Dependencies are updated every two weeks by a bot, a human merges the pull request.
- We keep a changelog per service, written for the support team.
- Build artifacts are signed and kept for ninety days.

## Context

The staging data is anonymised every night from a production snapshot. Error budget for the quarter is almost spent, slow down risky changes. The incident started with a config change that was not reviewed. Timeouts are set on every outgoing call, the default is two seconds. The health endpoint must not call other services, only check local state.

## Next steps

- Emma suggested splitting the {svc} service into a reader and a writer.
- Alerts page the on-call engineer only for user-facing symptoms.
