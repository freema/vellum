---
tags: [database, reference]
---
# Postgres connection pooling

Every Postgres connection is a process with its own memory, so hundreds of idle connections hurt.

## PgBouncer

- Transaction pooling mode gives the best reuse, but breaks session state: prepared statements, advisory locks, `SET` commands.
- Session pooling is safe but barely helps with many short requests.
- `default_pool_size` per database and user pair, start with 20.

## In the application

The Go `database/sql` pool is enough for a single service: set `SetMaxOpenConns`, `SetMaxIdleConns` and `SetConnMaxLifetime`. Size the pool from the number of CPU cores of the database, not from the number of requests.

Watch `pg_stat_activity` for connections stuck in `idle in transaction`.
