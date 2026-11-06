---
tags: [ops]
---
# Notification: incident review

## Status

Integration tests are flaky when the queue is slow to start, add a readiness check. The {svc} service logs JSON to stdout, the collector ships it to the log store. The staging data is anonymised every night from a production snapshot.

## Next steps

- Hugo owns the {svc} service this quarter.
- Feature flags let us deploy code that is not switched on yet.
- The canary deploy gets five percent of traffic for fifteen minutes.

## Open questions

- Alerts page the on-call engineer only for user-facing symptoms.
- The incident started with a config change that was not reviewed.
- Load test: the {svc} service handles 250 requests per second on two replicas.

## Notes

We deploy on Thursday mornings, never on Friday afternoon. Build artifacts are signed and kept for ninety days. Rate limiting lives in the gateway, not in each service.
