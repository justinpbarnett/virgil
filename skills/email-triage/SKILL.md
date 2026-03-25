---
description: "Triage unread email across all accounts: categorize into imbox/feed/paper_trail/noise and create tasks for emails needing a reply."
tools: [email_list, email_read, email_categorize, memory_store, task_create]
---

## Email triage

You are triaging Justin's email. Work through unread emails across all accounts and categorize each one.

### Categories

- **imbox** -- real email from a real person that may need a reply. Leave in inbox. Create a task if action is needed.
- **feed** -- newsletters, digests, content updates. Archive (remove from inbox).
- **paper_trail** -- receipts, confirmations, shipping notifications, bank statements. Archive.
- **noise** -- automated alerts, social notifications, marketing, spam. Archive.

### Process

1. Call `email_list` with `unread_only: true` for each account (personal, keep, enver, passion) or all at once.
2. For each email, read the snippet and headers. If the category is obvious from headers alone (newsletter, noreply, automated), categorize immediately without reading the full body.
3. If the category is unclear, call `email_read` on the thread.
4. Call `email_categorize` with the message ID, account, and chosen category.
5. If the email is imbox and likely needs a reply, call `task_create` with a brief description, source "email", and the message ID in source_id.
6. After processing, call `memory_store` with a brief observation summary: how many of each category, any notable senders or threads.

### Signals for imbox

- Email is from a real person (not noreply, not automated)
- Direct address to Justin (not a mailing list CC)
- Contains a question, request, or decision that needs Justin's involvement
- From a known contact (client, coworker, family)

### Signals for feed

- "Unsubscribe" link visible in snippet
- Sender is a publication, newsletter, or content platform
- Subject follows newsletter patterns ("This week in...", "Your digest", "Top stories")

### Signals for paper_trail

- Sender is a bank, retailer, shipping company, SaaS service
- Subject contains: receipt, invoice, confirmation, shipped, order, statement, payment

### Signals for noise

- Noreply sender with no direct content
- Social network notification (LinkedIn, Twitter, GitHub)
- Automated alert with no action required
- Marketing or promotional content

### Output

Return a brief summary: accounts processed, totals per category, any imbox tasks created, and any unusual patterns noticed.
