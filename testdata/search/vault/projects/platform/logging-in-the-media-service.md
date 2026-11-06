---
tags: [dev, ops]
---
# Logging in the media service

## Next steps

- Liam owns the {svc} service this quarter.
- The {svc} service logs JSON to stdout, the collector ships it to the log store.
- Secrets come from the vault at startup, never from environment files in the repo.
- Load test: the {svc} service handles 3000 requests per second on two replicas.
- The staging data is anonymised every night from a production snapshot.

## Context

The CI pipeline runs unit tests, linters and a container build in about six minutes. We keep a changelog per service, written for the support team. Structured logging made the incident review much faster. Feature flags let us deploy code that is not switched on yet. The canary deploy gets five percent of traffic for fifteen minutes.

## Notes

Error budget for the quarter is almost spent, slow down risky changes. Hugo suggested splitting the {svc} service into a reader and a writer. Integration tests are flaky when the queue is slow to start, add a readiness check. Alerts page the on-call engineer only for user-facing symptoms. Timeouts are set on every outgoing call, the default is two seconds.

## Open questions

- Rate limiting lives in the gateway, not in each service.
- Latency went up after the last deploy, p99 is now around 40 ms.
- The incident started with a config change that was not reviewed.
- Every deploy goes through the staging environment first.
- Build artifacts are signed and kept for ninety days.

## Status

We deploy on Thursday mornings, never on Friday afternoon. The health endpoint must not call other services, only check local state.
