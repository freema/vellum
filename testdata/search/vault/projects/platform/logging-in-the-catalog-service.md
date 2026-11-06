---
tags: [notes, engineering]
---
# Logging in the catalog service

The canary deploy gets five percent of traffic for fifteen minutes. Emma suggested splitting the {svc} service into a reader and a writer. Error budget for the quarter is almost spent, slow down risky changes.

Hugo owns the {svc} service this quarter. Load test: the {svc} service handles 15 requests per second on two replicas. Every deploy goes through the staging environment first.
