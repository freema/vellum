---
tags: [dev]
---
# Code review guidelines (auth)

## Open questions

- Code review needs one approval, two for changes in the payment path.
- Kate suggested splitting the {svc} service into a reader and a writer.
- Rate limiting lives in the gateway, not in each service.

## Decisions

Each deploy is tagged with the git commit, so tracing a regression is quick. Timeouts are set on every outgoing call, the default is two seconds. The {svc} service logs JSON to stdout, the collector ships it to the log store.

## Next steps

We keep a changelog per service, written for the support team. Structured logging made the incident review much faster. The health endpoint must not call other services, only check local state.

## Context

- Error budget for the quarter is almost spent, slow down risky changes.
- Integration tests are flaky when the queue is slow to start, add a readiness check.
- Load test: the {svc} service handles 5 requests per second on two replicas.

## Status

- Dependencies are updated every two weeks by a bot, a human merges the pull request.
- The CI pipeline runs unit tests, linters and a container build in about six minutes.
