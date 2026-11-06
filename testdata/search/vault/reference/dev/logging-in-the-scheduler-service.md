---
tags: [notes]
---
# Logging in the scheduler service

## Decisions

Integration tests are flaky when the queue is slow to start, add a readiness check. Code review needs one approval, two for changes in the payment path. Dependencies are updated every two weeks by a bot, a human merges the pull request.

## Open questions

Every deploy goes through the staging environment first. The canary deploy gets five percent of traffic for fifteen minutes. Rate limiting lives in the gateway, not in each service.

## Next steps

Latency went up after the last deploy, p99 is now around 25 ms. The {svc} service logs JSON to stdout, the collector ships it to the log store. Timeouts are set on every outgoing call, the default is two seconds.

## Status

The incident started with a config change that was not reviewed.
