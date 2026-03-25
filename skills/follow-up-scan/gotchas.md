# Follow-up scan gotchas

## Skip one-way sends

Newsletters, receipts, automated notifications, and marketing emails never need a reply. Only flag imbox emails (those with the virgil/imbox label or that look like real person-to-person communication). The label filter in the query handles most of this.

## JIRA notifications override meeting todos

If an action item from a meeting maps to an existing open JIRA ticket, don't create a separate task. The JIRA ticket is the canonical tracker. Check memory for recent JIRA observations before creating tasks for engineering action items.

## Don't nag on short gaps

A 2-hour-old email doesn't need a follow-up task. Threshold is 24 hours for standard imbox, 4 hours for anything marked urgent or from a VIP sender (client, employer, family).

## Suppress Telegram push when nothing was found

The scheduler pushes any non-empty string to Telegram. To suppress the notification when there are no follow-ups to report, return an empty string (or return nothing). Do not return "All clear." -- that string is non-empty and will be pushed.

## Avoid cascading tasks

If you already created a follow-up task for the same email thread last run, don't create another. The task will still be open in task_list -- check before creating.
