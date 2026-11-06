---
tags: [runbook, ops]
---
# Rollback runbook

When a deploy goes wrong and a fix forward is not possible within 15 minutes.

1. Announce the rollback in the incident channel.
2. Roll back the application: redeploy the previous image tag from the release list.
3. Database migrations: only roll back if the migration has a tested down step. Otherwise keep the schema and roll back the code only, our migrations are backward compatible by rule.
4. Verify the error rate in the dashboard for ten minutes.
5. Write a short note in the incident document.

Never roll back by hand-editing resources in the cluster, the next deploy would overwrite it anyway.
