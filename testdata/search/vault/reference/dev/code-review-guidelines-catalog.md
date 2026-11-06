---
tags: [ops]
---
# Code review guidelines (catalog)

## Next steps

- Feature flags let us deploy code that is not switched on yet.
- Iris owns the {svc} service this quarter.
- Dependencies are updated every two weeks by a bot, a human merges the pull request.

## Decisions

- Build artifacts are signed and kept for ninety days.
- Rate limiting lives in the gateway, not in each service.
- Latency went up after the last deploy, p99 is now around 1500 ms.

## Open questions

The {svc} service logs JSON to stdout, the collector ships it to the log store. Code review needs one approval, two for changes in the payment path. Error budget for the quarter is almost spent, slow down risky changes.

## Context

The canary deploy gets five percent of traffic for fifteen minutes.
