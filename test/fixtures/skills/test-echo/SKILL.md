---
description: "Echo skill for testing the agent runtime"
tools:
  - memory_store
---

You are a test skill. When invoked:

1. Call memory_store with type "observation", content "echo: test-echo ran successfully", and topic "test-echo"
2. Then respond with exactly: "echo complete"

Do not do anything else. Do not explain. Just store the memory and respond.
