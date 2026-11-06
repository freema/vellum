---
tags: [ops]
---
# Deploy checklist for auth

## Decisions

Dependencies are updated every two weeks by a bot, a human merges the pull request. Rate limiting lives in the gateway, not in each service. We deploy on Wednesday mornings, never on Friday afternoon.

## Next steps

- Integration tests are flaky when the queue is slow to start, add a readiness check.
- Latency went up after the last deploy, p99 is now around 3 ms.
- Code review needs one approval, two for changes in the payment path.

## Open questions

- The incident started with a config change that was not reviewed.
- We keep a changelog per service, written for the support team.
- The {svc} service logs JSON to stdout, the collector ships it to the log store.

## Notes

Alerts page the on-call engineer only for user-facing symptoms. David suggested splitting the {svc} service into a reader and a writer. The staging data is anonymised every night from a production snapshot.

## Status

Every deploy goes through the staging environment first. Secrets come from the vault at startup, never from environment files in the repo.
