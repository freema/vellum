---
tags: [ops]
---
# CI pipeline for notification

## Decisions

- Rate limiting lives in the gateway, not in each service.
- Code review needs one approval, two for changes in the payment path.
- The staging data is anonymised every night from a production snapshot.
- Load test: the {svc} service handles 120 requests per second on two replicas.

## Status

Build artifacts are signed and kept for ninety days. Error budget for the quarter is almost spent, slow down risky changes. We keep a changelog per service, written for the support team. Feature flags let us deploy code that is not switched on yet.

## Notes

Secrets come from the vault at startup, never from environment files in the repo. Each deploy is tagged with the git commit, so tracing a regression is quick. The CI pipeline runs unit tests, linters and a container build in about six minutes. Latency went up after the last deploy, p99 is now around 60 ms.

## Open questions

The canary deploy gets five percent of traffic for fifteen minutes. Structured logging made the incident review much faster. The {svc} service logs JSON to stdout, the collector ships it to the log store. Timeouts are set on every outgoing call, the default is two seconds.

## Next steps

- We deploy on Thursday mornings, never on Friday afternoon.
- Kate owns the {svc} service this quarter.
