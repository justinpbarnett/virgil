---
description: "One-time bootstrap: scan the last 30 days of email, calendar, Slack, JIRA, Drive, and Omi to build the initial fact base."
tools: [email_list, email_read, calendar_check, slack_read, slack_search, jira_search, drive_list, drive_read, omi_ingest, memory_store, memory_facts]
---

## Bootstrap scan

Build Justin's initial memory base from the last 30 days of data across all sources. This runs once to prime the system before ongoing skills take over.

### Goal

Extract durable facts about:
- People Justin works with regularly (names, roles, orgs, relationship context)
- Active projects (what they are, who's involved, current status)
- Recurring patterns (standing meetings, regular correspondents, typical workflows)
- Key contacts per org (clients, leadership, collaborators)

### Process

1. **Omi conversations**: Call `omi_ingest` with `since: "30 days ago"` to pull recent meetings and conversations into memory.

2. **Calendar**: Call `calendar_check` with `start: "30 days ago"`, `end: "today"` for each account. Note recurring meetings, frequent attendees, and project names in meeting titles.

3. **Email**: Call `email_list` for each account with `since: "30 days ago"`, `limit: 100`. Note frequent senders/recipients and their domains. For threads with 3+ messages, call `email_read` to get full context.

4. **Slack**: For each workspace, call `slack_search` with recent broad queries ("last month") or call `slack_read` on watch channels going back 30 days. Extract active projects, decisions made, and key contributors.

5. **JIRA**: Call `jira_search` with "assignee = currentUser() ORDER BY updated DESC" for each instance. Note active tickets, their projects, and collaborators.

6. **Drive**: Call `drive_list` with `limit: 50`. Note recent documents, their owners, and what projects they relate to.

7. **Synthesize facts**: For each person, project, and pattern you've identified, check `memory_facts` first to avoid overwriting. Then call `memory_store` with type "fact" for each durable insight:
   - Person facts: role, org, relationship to Justin, contact info if visible
   - Project facts: what it is, who owns it, current status, key stakeholders
   - Pattern facts: recurring meetings, workflows, how Justin typically interacts with a system

8. **Report**: After completing all sources, store a final observation: "Bootstrap completed on [date]. Processed X emails, Y calendar events, Z Slack messages, N JIRA tickets. Created P facts."

### What to store as facts vs. skip

Store as fact: recurring relationships, named projects, role information, preferences revealed through patterns.
Skip: one-off events with no recurrence, automated messages with no relationship context, noise.

### Output

Return a summary of what was processed per source, how many facts were created, and any gaps noticed (sources that had no data or returned errors).
