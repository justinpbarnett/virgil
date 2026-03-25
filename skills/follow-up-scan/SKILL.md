---
description: "Scan for dropped balls: emails needing a reply, stale tasks, and open action items from recent meetings or Slack threads."
tools: [email_list, task_list, memory_search, task_create]
---

## Follow-up scan

Find things that have fallen through the cracks and create tasks to address them.

### Process

1. **Unanswered emails**: Call `email_list` with `query: "is:inbox label:virgil/imbox"` across all accounts. For each imbox email older than 24 hours, check if Justin sent a reply (look for the thread_id in sent mail). If no reply and the email is from a real person, create a task: "Reply to <sender> re: <subject>", source "followup", priority based on sender importance.

2. **Stale tasks**: Call `task_list` with `status: "open"`. Flag tasks that have been open more than 7 days with no activity. For high-priority tasks older than 3 days, note them as at-risk.

3. **Recent meeting action items**: Call `memory_search` with "action item" and "follow up" to surface any action items captured from recent meetings or Slack observations. For each unresolved action item that doesn't already have a corresponding open task, create one.

4. **Deduplication**: Before creating any task, check existing open tasks to avoid duplicates. If a task already covers the same source_id or very similar content, skip it.

5. After scanning, call `memory_store` with an observation summarizing what was found.

### Output

Return a summary: how many unanswered emails found, how many stale tasks flagged, how many new follow-up tasks created. If nothing needed attention, say "All clear." Push to Telegram only if new tasks were created.
