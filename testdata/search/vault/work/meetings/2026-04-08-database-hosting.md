# Database hosting decision, 8 April 2026

Attendees: platform team, billing team.

## Context

The self-hosted Postgres for the billing API needs a major version upgrade and nobody wants to own the backups and failover any more.

## Options

1. Keep self-hosting, hire help for the upgrade.
2. Move to a managed Postgres service from our cloud provider.
3. Move to a serverless Postgres offering.

## Decision

We decided to move the database to the managed service (option 2). It costs about 30 % more, but backups, failover and minor upgrades are included. Serverless was rejected because of cold starts.

## Next steps

- Migration plan by the end of April
- Test restore from the managed backups before the switch
