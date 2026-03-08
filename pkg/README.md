# Virgil Public API

Public interfaces and types for building on Virgil. These packages define the contracts that external code (including virgil-cloud) programs against.

## Packages

| Package | Purpose |
|---|---|
| `envelope` | Universal data contract between pipes |
| `pipe` | Handler signatures, definition types, trigger/flag types |
| `protocol` | Subprocess communication protocol (stdin JSON request, stdout response) |
| `bridge` | AI provider interface for custom model backends |
| `memory` | Memory backend interface (swap SQLite for Postgres, etc.) |
| `server` | Server middleware interface for auth, billing, rate limiting |
| `router` | Pipe/pipeline registry interface |

## Current Status

During the single-module phase, concrete types remain in `internal/`. These `pkg/` packages define the target API. When virgil-core and virgil-cloud split into separate modules, types will migrate from `internal/` to `pkg/` and become the canonical imports.
