---
description: "Prepare context for today's upcoming meetings: pull attendee facts, relevant emails, and recent history for each event."
tools: [calendar_check, memory_search, memory_facts, email_list]
---

## Calendar prep

Prepare Justin for today's upcoming meetings by assembling context for each one.

### Process

1. Call `calendar_check` with `start: "today"` to get today's events across all accounts.
2. Filter to meetings (events with attendees, not solo blocks).
3. For each meeting:
   a. Extract attendee names and emails.
   b. Call `memory_facts` for each attendee by name to pull known facts (role, company, relationship, history).
   c. Call `memory_search` with the meeting title and attendee names to find relevant past interactions and observations.
   d. Call `email_list` with `from: <attendee email>` and `since: "7 days ago"` to find recent correspondence.
   e. Assemble a brief for this meeting using the template below.
4. If there are no meetings today, say so and check tomorrow.

### Meeting brief template

For each meeting, produce output following this structure (from templates/meeting-brief.md):

{{meeting-brief.md}}

### Output

Return a brief for each meeting, separated by `---`. Push this as a Telegram notification so Justin sees it before his first meeting.
