---
tags: [ops]
---
# Logging in the notification service

Load test: the {svc} service handles 20 requests per second on two replicas. Timeouts are set on every outgoing call, the default is two seconds. The {svc} service logs JSON to stdout, the collector ships it to the log store.

Error budget for the quarter is almost spent, slow down risky changes.
