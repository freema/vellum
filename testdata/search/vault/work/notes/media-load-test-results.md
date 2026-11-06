---
tags: [notes, engineering]
---
# Media: load test results

## Status

Integration tests are flaky when the queue is slow to start, add a readiness check. Emma owns the {svc} service this quarter. The CI pipeline runs unit tests, linters and a container build in about six minutes.

## Next steps

Every deploy goes through the staging environment first. Error budget for the quarter is almost spent, slow down risky changes. Timeouts are set on every outgoing call, the default is two seconds.

## Notes

Code review needs one approval, two for changes in the payment path. We keep a changelog per service, written for the support team. Alerts page the on-call engineer only for user-facing symptoms.

## Decisions

Feature flags let us deploy code that is not switched on yet. Rate limiting lives in the gateway, not in each service. Structured logging made the incident review much faster.

## Open questions

Dependencies are updated every two weeks by a bot, a human merges the pull request. Load test: the {svc} service handles 250 requests per second on two replicas.
