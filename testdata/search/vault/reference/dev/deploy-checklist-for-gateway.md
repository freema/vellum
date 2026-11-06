---
tags: [ops]
---
# Deploy checklist for gateway

## Context

Build artifacts are signed and kept for ninety days. The canary deploy gets five percent of traffic for fifteen minutes. The incident started with a config change that was not reviewed.

## Status

Alice owns the {svc} service this quarter. Each deploy is tagged with the git commit, so tracing a regression is quick. Rate limiting lives in the gateway, not in each service.

## Next steps

- Error budget for the quarter is almost spent, slow down risky changes.
- Dependencies are updated every two weeks by a bot, a human merges the pull request.
