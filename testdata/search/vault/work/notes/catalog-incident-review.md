---
tags: [dev, engineering]
---
# Catalog: incident review

## Notes

- Feature flags let us deploy code that is not switched on yet.
- Secrets come from the vault at startup, never from environment files in the repo.
- Jonas suggested splitting the {svc} service into a reader and a writer.
- Jonas owns the {svc} service this quarter.
- The CI pipeline runs unit tests, linters and a container build in about six minutes.
- The {svc} service logs JSON to stdout, the collector ships it to the log store.
- Code review needs one approval, two for changes in the payment path.
- The incident started with a config change that was not reviewed.
- Load test: the {svc} service handles 250 requests per second on two replicas.

## Status

Each deploy is tagged with the git commit, so tracing a regression is quick. Latency went up after the last deploy, p99 is now around 250 ms. Timeouts are set on every outgoing call, the default is two seconds. Alerts page the on-call engineer only for user-facing symptoms. Every deploy goes through the staging environment first. We keep a changelog per service, written for the support team. Structured logging made the incident review much faster. We deploy on Monday mornings, never on Friday afternoon. Build artifacts are signed and kept for ninety days.

## Next steps

- Dependencies are updated every two weeks by a bot, a human merges the pull request.
- Integration tests are flaky when the queue is slow to start, add a readiness check.
- The canary deploy gets five percent of traffic for fifteen minutes.
- The health endpoint must not call other services, only check local state.
- Rate limiting lives in the gateway, not in each service.
- The staging data is anonymised every night from a production snapshot.
- Error budget for the quarter is almost spent, slow down risky changes.
- Dependencies are updated every two weeks by a bot, a human merges the pull request.
- Timeouts are set on every outgoing call, the default is two seconds.

## Decisions

- Load test: the {svc} service handles 20 requests per second on two replicas.
- Integration tests are flaky when the queue is slow to start, add a readiness check.
- Alerts page the on-call engineer only for user-facing symptoms.
- Build artifacts are signed and kept for ninety days.
- The health endpoint must not call other services, only check local state.
- Build artifacts are signed and kept for ninety days.
- Error budget for the quarter is almost spent, slow down risky changes.
- Latency went up after the last deploy, p99 is now around 400 ms.
- Code review needs one approval, two for changes in the payment path.
