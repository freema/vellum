---
tags: [engineering, dev]
---
# Payments gateway: incident review

Latency went up after the last deploy, p99 is now around 800 ms. The CI pipeline runs unit tests, linters and a container build in about six minutes. Every deploy goes through the staging environment first.

Integration tests are flaky when the queue is slow to start, add a readiness check. Alerts page the on-call engineer only for user-facing symptoms. Error budget for the quarter is almost spent, slow down risky changes.
