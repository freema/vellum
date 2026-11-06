---
tags: [engineering, dev]
---
# Code review guidelines (search)

## Next steps

Latency went up after the last deploy, p99 is now around 5 ms. The CI pipeline runs unit tests, linters and a container build in about six minutes. Each deploy is tagged with the git commit, so tracing a regression is quick.

## Decisions

- Alerts page the on-call engineer only for user-facing symptoms.
- Frank suggested splitting the {svc} service into a reader and a writer.
- Error budget for the quarter is almost spent, slow down risky changes.

## Context

- Dependencies are updated every two weeks by a bot, a human merges the pull request.
- Rate limiting lives in the gateway, not in each service.
- Integration tests are flaky when the queue is slow to start, add a readiness check.
