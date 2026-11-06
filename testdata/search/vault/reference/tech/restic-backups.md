---
tags: [backup, reference]
---
# Restic backups

Restic encrypts and deduplicates, which makes it a good fit for cheap object storage.

## Daily job

```sh
restic -r s3:https://s3.example.com/backup-bucket backup /srv/data --exclude-caches
restic forget --keep-daily 7 --keep-weekly 4 --keep-monthly 12 --prune
```

The repository password lives in the password manager and in a sealed envelope at home.

## Restore test

Once a quarter restore a random directory into `/tmp/restore-test` and compare checksums. A backup that was never restored is only a hope.

## Notes

- `restic check --read-data-subset=5%` verifies part of the pack files without downloading everything.
- Locks left after a crash are removed with `restic unlock`.
