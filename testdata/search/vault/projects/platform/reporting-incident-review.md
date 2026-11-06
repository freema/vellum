---
tags: [dev, engineering]
---
# Reporting: incident review

We deploy on Wednesday mornings, never on Friday afternoon. Latency went up after the last deploy, p99 is now around 40 ms. The CI pipeline runs unit tests, linters and a container build in about six minutes.

The incident started with a config change that was not reviewed. Grace owns the {svc} service this quarter. Rate limiting lives in the gateway, not in each service.
