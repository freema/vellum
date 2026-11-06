---
tags: [dev]
---
# Code review guidelines (notification)

## Open questions

Alerts page the on-call engineer only for user-facing symptoms. We deploy on Thursday mornings, never on Friday afternoon. Code review needs one approval, two for changes in the payment path.

## Notes

Error budget for the quarter is almost spent, slow down risky changes. Load test: the {svc} service handles 250 requests per second on two replicas. Each deploy is tagged with the git commit, so tracing a regression is quick.

## Context

- Timeouts are set on every outgoing call, the default is two seconds.
- We keep a changelog per service, written for the support team.
- Feature flags let us deploy code that is not switched on yet.

## Status

Dependencies are updated every two weeks by a bot, a human merges the pull request. Rate limiting lives in the gateway, not in each service. Frank suggested splitting the {svc} service into a reader and a writer.

## Next steps

- The {svc} service logs JSON to stdout, the collector ships it to the log store.
- The staging data is anonymised every night from a production snapshot.
