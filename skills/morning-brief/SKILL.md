---
description: "Generate a daily morning brief: today's calendar, unread email counts, open tasks, and anything notable from overnight Slack."
tools: [calendar_check, email_list, task_list, memory_search, slack_read]
---

## Morning brief

Compile Justin's morning brief covering what's happening today and what needs attention.

### Process

1. **Calendar**: Call `calendar_check` with `start: "today"`, `days: 1`. List all events for today with times and key attendees.

2. **Email**: Call `email_list` with `unread_only: true` for each account. Count unread per account. Note any subject lines that look urgent or important (flagged, from key contacts, time-sensitive subject).

3. **Tasks**: Call `task_list` with `status: "open"`. Highlight urgent and high-priority tasks. Flag any tasks due today.

4. **Slack overnight**: Call `slack_read` for each configured workspace's watch channels with `since: "yesterday"`. Summarize anything notable -- decisions made, things that need Justin's input, or context he should have.

5. **Memory context**: Call `memory_search` with "today priorities" to surface any relevant facts or upcoming commitments from memory.

6. Assemble using the brief template.

### Output format

Use the template at templates/brief.md. Be concise -- this gets sent to Telegram. No padding. Just the signal.
