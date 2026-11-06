---
tags: [dev]
---
# Notification service design

## Context

- The CI pipeline runs unit tests, linters and a container build in about six minutes.
- Every deploy goes through the staging environment first.
- The staging data is anonymised every night from a production snapshot.

## Status

Structured logging made the incident review much faster. Feature flags let us deploy code that is not switched on yet. Latency went up after the last deploy, p99 is now around 15 ms.

## Decisions

The canary deploy gets five percent of traffic for fifteen minutes. Dependencies are updated every two weeks by a bot, a human merges the pull request.
