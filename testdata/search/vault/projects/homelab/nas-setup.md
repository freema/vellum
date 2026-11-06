---
tags: [homelab, storage]
---
# NAS setup

Two 8 TB disks in a mirror, ZFS on a small Intel board.

Datasets: `photos`, `documents`, `media`, `vm`. Snapshots every hour, kept for two days, daily ones for a month.

Offsite backup: restic runs every night from the NAS to object storage, see the restic note for the commands. Media is excluded from the offsite backup, it can be downloaded again.

Power draw at idle is about 18 W. Disks spin down after 30 minutes.
