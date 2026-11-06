---
tags: [engineering]
---
# Logging in the search service

## Context

Error budget for the quarter is almost spent, slow down risky changes. We deploy on Wednesday mornings, never on Friday afternoon. Secrets come from the vault at startup, never from environment files in the repo.

## Open questions

- Build artifacts are signed and kept for ninety days.
- The staging data is anonymised every night from a production snapshot.
- Structured logging made the incident review much faster.

## Notes

David suggested splitting the {svc} service into a reader and a writer. Timeouts are set on every outgoing call, the default is two seconds. Rate limiting lives in the gateway, not in each service.

## Next steps

Latency went up after the last deploy, p99 is now around 120 ms. Alerts page the on-call engineer only for user-facing symptoms. Integration tests are flaky when the queue is slow to start, add a readiness check.

## Decisions

- The {svc} service logs JSON to stdout, the collector ships it to the log store.
