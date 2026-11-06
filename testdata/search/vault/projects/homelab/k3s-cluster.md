---
title: Lightweight cluster at home
tags: [homelab]
---
Three Raspberry Pi 5 boards running a small Kubernetes distribution, one server node and two agents.

## Why this setup

The full control plane is too heavy for 8 GB boards. This one ships as a single binary, uses SQLite instead of etcd by default and starts in under a minute.

## Install notes

1. Flash Raspberry Pi OS Lite, enable cgroups in `cmdline.txt`.
2. Run the install script on the server node, copy the node token.
3. Join both agents with the token and the server URL.
4. Copy the kubeconfig to the laptop and rename the context to `pi`.

Traefik comes bundled; I disabled it because the reverse proxy runs outside the cluster.

## Open issues

- The SD cards wear out, move the server node to an SSD.
- Longhorn is too heavy, local-path storage is enough for now.
