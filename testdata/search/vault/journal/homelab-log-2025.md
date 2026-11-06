---
tags: [homelab, log]
---
# Homelab log 2025

## January

- 04.01. Replaced the old router, moved the WireGuard server to it. Config copied over, peers kept their keys.
- 11.01. Grafana dashboard for the UPS.
- 19.01. NAS scrub found no errors.
- 26.01. WireGuard peer for the new laptop.

## February

- 02.02. Pi-hole update, blocklists trimmed.
- 09.02. Power outage, everything came back except the WireGuard tunnel, the router started before the modem got an address.
- 16.02. Added a startup delay to WireGuard on the router.
- 23.02. Home Assistant moved into a VM.

## March

- 02.03. Tried Tailscale next to WireGuard, kept WireGuard for now.
- 16.03. Replaced a failing fan in the NAS.
- 30.03. New VLAN for IoT devices, WireGuard clients can reach it.

## April

- 06.04. Certificates on the reverse proxy renewed fine.
- 13.04. Phone could not connect over WireGuard on mobile data, MTU again.
- 27.04. Backup restore test, all good.

## May to August

- 11.05. Bought a second UPS.
- 08.06. WireGuard on the travel router for the holiday.
- 20.07. Nothing broke during the holiday.
- 17.08. Cleaned the dust out of the rack.

## September to December

- 07.09. Monitoring alert for disk temperature.
- 12.10. Rebuilt the WireGuard config with a generator script, one file per peer.
- 23.11. Upgraded the cluster nodes.
- 21.12. Year in review: WireGuard was the most touched thing all year.
