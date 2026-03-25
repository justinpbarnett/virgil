---
description: "Scan configured Slack channels for messages that need attention, action items, or are worth remembering."
tools: [slack_read, slack_search, memory_store, task_create]
---

## Slack scan

Scan recent Slack activity across all configured workspaces and flag anything that needs Justin's attention.

### Process

1. For each configured workspace (passion, enver), call `slack_read` on the watch channels with `since: "4 hours ago"` (or since last run if you can infer it from recent observations).
2. Review messages for:
   - Direct @mentions of Justin
   - Questions or decisions directed at Justin
   - Action items or assignments ("Justin can you...", "assigning to Justin")
   - Significant announcements or decisions that affect Justin's work
   - Threads Justin is part of that have new replies
3. For anything that needs Justin's response, call `task_create` with brief context, source "slack", and the channel + timestamp in source_id.
4. For interesting context (team decisions, project updates, things Justin should know but doesn't need to act on), call `memory_store` as an observation with the relevant scope (bridge:passion or bridge:enver).
5. Ignore: bot messages, automated CI/CD notifications, random chatter not involving Justin.

### Output

Return a brief summary: workspaces and channels scanned, any tasks created (with channel/thread context), any notable observations stored. If nothing needed attention, say so clearly.
