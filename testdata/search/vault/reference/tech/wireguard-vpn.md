---
tags: [network, homelab]
---
# WireGuard VPN

Access to the home network from the phone and the laptop.

- Server runs on the router, UDP port 51820.
- Every device has its own key pair and an address from `10.8.0.0/24`.
- Split tunnel: only the home subnet goes through the tunnel.

## Troubleshooting

- No handshake: check the port forward and that the public key on the server matches.
- Handshake but no traffic: `AllowedIPs` on one side is wrong.
- Works on Wi-Fi but not on mobile data: lower the MTU to 1280.
