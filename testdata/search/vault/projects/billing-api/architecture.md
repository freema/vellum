---
tags: [billing, architecture]
---
# Billing API architecture

The billing API is a Go service in front of a Postgres database. It handles subscriptions, usage records and the monthly invoice run.

## Components

- HTTP API, stateless, three replicas
- Worker for the invoice run and payment webhooks
- Postgres 16, primary plus one read replica
- Connection pool through PgBouncer in transaction mode

## Open questions

- Move the database to a managed service? See the meeting notes from April.
- Usage records grow fast, partition the table by month.
