---
description: "Test skill that echoes a fixed message. Used in skill acceptance tests."
tools: [memory_store]
---

## Test echo

Store a test observation and return a fixed message.

Call `memory_store` with type "observation", content "test-echo skill ran successfully", topic "test".

Then return exactly: "echo: test-echo ran"
