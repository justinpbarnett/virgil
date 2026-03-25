# Email triage gotchas

## Don't re-triage already-categorized emails

If an email already has a virgil/* label, skip it. The email_list snippet or labels field will show existing categorizations. The query "is:unread" already filters most of these out, but watch for threads where only the latest message is unread.

## GitHub and JIRA notifications are noise, not feed

They're automated but not newsletters. Category: noise. Exception: if the notification is a direct @mention or assignment to Justin, treat as imbox.

## Calendar invites are paper_trail

Invitations from Google Calendar that are purely informational (already accepted or declined) go to paper_trail. If an invite requires Justin's decision, treat it as imbox.

## Don't create duplicate tasks

If a task already exists with the same source_id (message ID), don't create another. The task_create tool deduplicates by source_id, but double-check before creating.

## Passion email has higher noise floor

The passion (Passion City Church) account gets a lot of automated Rock RMS notifications and internal church systems. Apply a higher filter for noise -- automated internal systems go to paper_trail or noise, not imbox, unless directly addressed to Justin.

## Batch per account

Process one account at a time if total unread is over 50. This avoids token limits and keeps the run focused.
