---
tags: [dev]
---
# Deploy checklist for payments gateway

## Decisions

The {svc} service logs JSON to stdout, the collector ships it to the log store. Structured logging made the incident review much faster. The canary deploy gets five percent of traffic for fifteen minutes.

## Status

Latency went up after the last deploy, p99 is now around 3 ms. Integration tests are flaky when the queue is slow to start, add a readiness check. Feature flags let us deploy code that is not switched on yet.

## Context

Each deploy is tagged with the git commit, so tracing a regression is quick. Kate owns the {svc} service this quarter. Every deploy goes through the staging environment first.

## Open questions

- Error budget for the quarter is almost spent, slow down risky changes.
- Secrets come from the vault at startup, never from environment files in the repo.
