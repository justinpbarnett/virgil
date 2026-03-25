Good morning. Here's {{date}}.

## Today

{{#each events}}
- {{start_time}} - {{title}}{{#if location}} @ {{location}}{{/if}}
{{/each}}

{{#if no_events}}
No meetings today.
{{/if}}

## Email

{{#each accounts}}
- **{{name}}**: {{unread}} unread
{{/each}}

{{#if notable_emails}}
Notable:
{{#each notable_emails}}
- {{subject}} from {{from}}
{{/each}}
{{/if}}

## Tasks

{{#each urgent_tasks}}
- [URGENT] {{title}}
{{/each}}
{{#each high_tasks}}
- [HIGH] {{title}}
{{/each}}
{{open_count}} open total.

## Overnight Slack

{{slack_summary}}
