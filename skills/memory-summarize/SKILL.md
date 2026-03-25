---
description: "Summarize old observations into distilled facts to keep memory compact and relevant."
tools: [memory_search, memory_store]
---

## Memory summarization

Compact old memory by summarizing clusters of related observations into durable facts.

### Process

1. Call `memory_search` with an empty or broad query to fetch recent observations (type: "observation"), looking for entries older than 7 days.

2. Group observations by topic or entity. Look for clusters: multiple observations about the same person, project, or theme within a short time window.

3. For each cluster of 3 or more related observations, synthesize them into a single fact:
   - Distill what is durably true (not just what happened once)
   - Use the entity name as the topic
   - Write the fact in present tense ("Justin works with Alex on the Embers project" not "on March 5 Justin and Alex discussed Embers")
   - Include the most important details and relationships

4. Call `memory_store` with type "fact", the synthesized content, and the relevant entities extracted from the observations.

5. Single observations with no related cluster: leave them alone. Only summarize when there's a pattern worth distilling.

### What makes a good fact vs. a good observation

- **Fact**: "Justin's primary JIRA instance is passion.atlassian.net. He works on worship tech and volunteer management tickets."
- **Observation**: "Justin reviewed JIRA ticket PTP-1234 about check-in redesign on March 10."

Facts are timeless. Observations are dated. When a series of observations reveals a pattern, that pattern becomes a fact.

### Output

Return a brief report: how many observation clusters processed, how many facts created, any notable patterns found.
