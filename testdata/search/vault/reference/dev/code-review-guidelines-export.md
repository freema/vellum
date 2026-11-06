---
tags: [notes, ops]
---
# Code review guidelines (export)

Alerts page the on-call engineer only for user-facing symptoms. The incident started with a config change that was not reviewed. Structured logging made the incident review much faster.

The {svc} service logs JSON to stdout, the collector ships it to the log store. The canary deploy gets five percent of traffic for fifteen minutes.
