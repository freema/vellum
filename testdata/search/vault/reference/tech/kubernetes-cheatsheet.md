---
tags: [devops, reference]
---
# Kubernetes cheatsheet

Commands I keep looking up when working with Kubernetes clusters.

## Context and namespaces

- `kubectl config get-contexts` lists every cluster in the kubeconfig.
- `kubectl config use-context staging` switches the active cluster.
- `kubectl get pods -n monitoring` scopes a command to one namespace.

## Debugging pods

- `kubectl describe pod <name>` shows events, which is where image pull errors and failed probes show up.
- `kubectl logs -f deploy/api --since=10m` follows the logs of every pod behind a deployment.
- `kubectl exec -it <pod> -- sh` opens a shell; distroless images do not have one, use an ephemeral debug container instead.

## Rollouts

- `kubectl rollout status deploy/api` waits until the new ReplicaSet is ready.
- `kubectl rollout undo deploy/api` goes back to the previous revision.

Kubernetes resource requests decide scheduling, limits decide throttling. Set requests from real usage, not guesses.
