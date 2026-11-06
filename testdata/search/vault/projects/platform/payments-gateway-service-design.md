---
tags: [engineering]
---
# Payments gateway service design

## Notes

Latency went up after the last deploy, p99 is now around 20 ms. Dependencies are updated every two weeks by a bot, a human merges the pull request. Every deploy goes through the staging environment first.

## Open questions

The canary deploy gets five percent of traffic for fifteen minutes. The health endpoint must not call other services, only check local state. Timeouts are set on every outgoing call, the default is two seconds.

## Decisions

We keep a changelog per service, written for the support team. Hugo suggested splitting the {svc} service into a reader and a writer.
