---
tags: [ops]
---
# CI pipeline for scheduler

## Open questions

The staging data is anonymised every night from a production snapshot. Code review needs one approval, two for changes in the payment path. Dependencies are updated every two weeks by a bot, a human merges the pull request.

## Notes

The incident started with a config change that was not reviewed. Integration tests are flaky when the queue is slow to start, add a readiness check. Structured logging made the incident review much faster.

## Decisions

- Latency went up after the last deploy, p99 is now around 15 ms.
- We keep a changelog per service, written for the support team.
- Feature flags let us deploy code that is not switched on yet.

## Context

- We deploy on Thursday mornings, never on Friday afternoon.
- The canary deploy gets five percent of traffic for fifteen minutes.
- Build artifacts are signed and kept for ninety days.

## Next steps

Load test: the {svc} service handles 3 requests per second on two replicas. Each deploy is tagged with the git commit, so tracing a regression is quick.
