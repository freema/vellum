---
tags: [notes, ops]
---
# Payments gateway: load test results

The health endpoint must not call other services, only check local state. The canary deploy gets five percent of traffic for fifteen minutes. Latency went up after the last deploy, p99 is now around 1500 ms.

The incident started with a config change that was not reviewed. We keep a changelog per service, written for the support team. Emma suggested splitting the {svc} service into a reader and a writer.
