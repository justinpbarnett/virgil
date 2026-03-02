# Study Pipe Specification

This document defines the `study` pipe — Virgil's context assembly engine. It gathers the most relevant information from a source within a token budget, using deterministic methods first and AI compression only as a final resort.

For the pipe contract and format, see `pipe.md`. For context assembly architecture, see `ARCHITECTURE.md` decisions #24-26.

---

## Why This Is a Pipe, Not a Pipeline

The internal stages of context gathering — discover, extract, rank, compress — are tightly coupled. Ranking changes what you extract. Extraction results change compression decisions. The budget constraint creates feedback between stages: if ranking surfaces 40 candidates but only 12 fit the budget, the compression tier determines whether you include 12 uncompressed or 25 compressed. These decisions are interdependent.

Breaking them into separate pipes would shuttle intermediate state through envelopes for no compositional benefit. Nobody chains `study.rank` independently into a different workflow. The "one thing" this pipe does is **assemble relevant context within a token budget**. The internal stages are implementation details, same as `draft` internally resolves templates, assembles prompts, calls a provider, and formats output — all within one pipe.

If internal complexity grows beyond what a single handler can maintain cleanly, the decomposition path is obvious: `study.discover`, `study.extract`, `study.rank`, `study.compress` as a named pipeline. But start with a pipe.

---

## What It Does

The `study` pipe takes a query (what context is needed) and a source (where to look), then returns the most relevant content compressed to fit within a token budget.

The key insight: **most agents gather context by grepping, reading entire files, and feeding everything to an LLM.** This burns tokens on irrelevant content and produces bloated context that degrades downstream AI quality. The study pipe inverts this — it uses structural understanding of the source to extract precisely the information that matters, and only invokes AI when deterministic compression isn't enough to meet the budget.

The philosophy mirrors the router: deterministic first, AI as fallback. The goal is for AI to fire less and less as the deterministic extraction layers get smarter.

---

## Definition

```yaml
name: study
description: Gathers and compresses relevant context from a source within a token budget.
category: research

triggers:
  exact:
    - "study the codebase"
    - "gather context"
  keywords:
    - study
    - context
    - gather
    - understand
    - analyze
    - codebase
  patterns:
    - "study {source}"
    - "study {source} for {topic}"
    - "gather context from {source} about {topic}"
    - "understand {topic} in {source}"

flags:
  source:
    description: What to study.
    values: [codebase, memory, files, web]
    default: codebase

  role:
    description: Perspective shaping what's relevant. A builder needs implementation details. A reviewer needs interfaces and contracts. A planner needs architecture.
    values: [builder, reviewer, planner, debugger, general]
    default: general

  budget:
    description: Maximum token count for the output context.
    default: "8000"
    required: false

  depth:
    description: How deep to follow the dependency graph from the entry point.
    values: [shallow, normal, deep]
    default: normal

  entry:
    description: Starting point — a file path, symbol name, directory, or query string.
    default: null
    required: false

  compression:
    description: Maximum compression tier allowed. Caps how aggressively the pipe compresses.
    values: [none, structural, ai]
    default: ai

vocabulary:
  verbs:
    study: study
    gather: study
    understand: study
    analyze: study
    investigate: study
  types: {}
  sources:
    codebase: codebase
    code: codebase
    repo: codebase
    files: files
    docs: files
    notes: memory
  modifiers:
    shallow: shallow
    deep: deep
```

---

## Internal Architecture

The handler operates in four stages. Each stage narrows and refines. The flow is always the same regardless of source type — only the backends differ.

```
query + source + budget
    │
    ▼
Stage 1: Discover
    Find candidate content items from the source.
    Deterministic. Fast. Over-inclusive by design.
    │
    ▼
Stage 2: Extract
    Pull the actual content for each candidate.
    Deterministic. Structural awareness determines WHAT to extract.
    │
    ▼
Stage 3: Rank
    Score every extracted item by relevance to the query.
    Deterministic. Multi-signal scoring.
    │
    ▼
Stage 4: Compress
    Fit the top-ranked content into the token budget.
    Layered: structural compression first, AI summarization last.
    │
    ▼
output envelope (content fits budget, maximally relevant)
```

### Stage 1: Discover

Discovery answers: **what content items might be relevant?** It casts a wide net. False positives are fine — ranking filters them. False negatives are the real danger.

Discovery is always deterministic. It uses different backends depending on the source:

**Codebase source** — three discovery methods run in parallel and their results merge:

1. **LSP semantic discovery.** This is the primary method for codebases. Start from the entry point (file, symbol, or query) and walk the semantic graph:
   - `workspace/symbol` — find symbols matching the query by name
   - `textDocument/definition` — resolve definitions
   - `textDocument/references` — find all usage sites
   - `callHierarchy/incomingCalls` and `outgoingCalls` — traverse the call graph
   - `textDocument/typeDefinition` — find type definitions for values
   - `textDocument/implementation` — find interface implementations

   Depth is controlled by the `--depth` flag. Shallow stops at direct references. Normal follows one level of the call graph. Deep follows two levels and includes transitive dependencies.

   **Why LSP over grep:** LSP understands the semantic structure of the code. `grep "UserService"` finds every string match, including comments, test fixtures, and unrelated variables named `userServiceCount`. LSP's `textDocument/references` finds every actual usage of the `UserService` type — and nothing else. The signal-to-noise ratio is categorically different.

2. **Structural index.** A pre-built index of the codebase's file and directory structure, package graph, and exported symbol table. This catches relationships LSP misses — configuration files that reference a module by string, README sections that describe a component, test files that exercise the code. The index is built at startup and updated on file change events.

3. **Text search.** Keyword search against a full-text index of the codebase. This catches semantic relationships that neither LSP nor the structural index encode — a comment explaining why a function exists, a commit message describing why code was changed, a doc that references a concept by a different name than the code uses. This is the broadest, noisiest method — it exists to catch what the other two miss.

**Memory source** — uses Virgil's existing memory retrieval system:

- Topic-based retrieval from the memory store
- Recency-weighted search
- Tag and metadata filtering
- Hierarchical summary levels (raw → daily → weekly → monthly)

**File source** — filesystem-based:

- Filename and path matching
- Full-text search within files (FTS5 or ripgrep)
- File metadata (modified date, size, type)
- Directory structure analysis

**Web source** — external search:

- Search API queries
- URL-specific fetching
- Result deduplication and source quality scoring

Each discovery method returns a list of **candidate items**, each with:

```
candidate:
  id:        unique identifier (file:line, memory:id, url, etc.)
  source:    which discovery method found it
  location:  where in the source (path, line range, URL)
  preview:   a lightweight preview (first N chars, title, symbol name)
  size_hint: estimated token count if fully extracted
```

Candidates are deduplicated by `id` before passing to extraction.

### Stage 2: Extract

Extraction answers: **what content do I actually pull for each candidate?** This is where structural awareness pays off. The pipe doesn't read entire files — it extracts the relevant slice at the appropriate granularity.

**Codebase extraction** uses AST-aware parsing (tree-sitter or the language's own AST library) to extract at the right granularity:

| Granularity      | What's extracted                                              | When used                                         |
| ---------------- | ------------------------------------------------------------- | ------------------------------------------------- |
| **Signature**    | Function/method signature + docstring, no body                | Default for call graph neighbors, depth > 1       |
| **Interface**    | Type/interface definition with all method signatures          | When a type was referenced, not a specific method |
| **Declaration**  | Full function/method body                                     | Entry point, direct references at depth 0-1       |
| **Block**        | A contiguous range of lines (e.g., a config block, test case) | Non-code files, structural index hits             |
| **File summary** | Package declaration, imports, exported symbols list           | Files at the edge of the dependency graph         |

The granularity is selected per-candidate based on:

1. **Distance from entry point.** Closer = more detail. The entry point file gets full declarations. One hop away gets declarations for referenced symbols, signatures for everything else. Two hops away gets signatures only.

2. **Role flag.** A builder gets implementation detail (full declarations, internal logic). A reviewer gets contracts and interfaces (signatures, types, test assertions). A debugger gets execution paths (call chains, error handling, state mutations). A planner gets architecture (package structure, interfaces, dependency graph).

3. **Information density.** An interface definition with 10 methods is 15 lines and tells you everything about a component's contract. The implementation behind those methods is 500 lines and mostly tells you how, not what. At the same token cost, the interface is 30x more information-dense for most downstream tasks. The pipe prefers higher-density extractions.

**Role-based extraction profiles:**

```
builder:
  entry_point:    declaration (full body)
  depth_0:        declaration
  depth_1:        declaration (referenced symbols), signature (others)
  depth_2:        signature
  include:        implementation details, internal state, helper functions
  exclude:        test files (unless entry point is a test)

reviewer:
  entry_point:    declaration (full body)
  depth_0:        interface + signature
  depth_1:        interface + signature
  depth_2:        file_summary
  include:        interfaces, contracts, test assertions, error types
  exclude:        implementation internals, helper functions

debugger:
  entry_point:    declaration (full body)
  depth_0:        declaration (focus: error handling, state mutations)
  depth_1:        declaration (call chain nodes only)
  depth_2:        signature
  include:        error types, logging calls, state transitions, recent git blame
  exclude:        unrelated code paths

planner:
  entry_point:    file_summary + interface
  depth_0:        interface
  depth_1:        file_summary
  depth_2:        package list only
  include:        package structure, dependency graph, architecture docs
  exclude:        implementation details, individual function bodies
```

Each extracted item carries:

```
extracted:
  id:           candidate id
  content:      the actual extracted text
  granularity:  which level was used
  token_count:  actual token count (measured, not estimated)
  metadata:
    language:   programming language
    path:       file path
    lines:      start-end line range
    symbol:     symbol name (if applicable)
    kind:       function | type | interface | method | const | var | block | file
    distance:   hops from entry point in the dependency graph
    modified:   last git modification date
```

### Stage 3: Rank

Ranking answers: **in what order should content fill the budget?** Every extracted item gets a composite relevance score.

The ranking is fully deterministic. No AI. Multiple signals combine into a single score:

**Signals:**

| Signal                    | Weight     | What it measures                                                                                                                                                                                   |
| ------------------------- | ---------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Semantic proximity**    | High       | Distance from entry point in the LSP/dependency graph. Closer = more relevant.                                                                                                                     |
| **Query match**           | High       | How well the item's content matches the query string. Measured by FTS5 rank or BM25 against the extracted text.                                                                                    |
| **Structural centrality** | Medium     | How many other candidates reference or depend on this item. High centrality = architectural importance. Hub nodes in the dependency graph.                                                         |
| **Information density**   | Medium     | Ratio of unique identifiers (types, functions, concepts) to total tokens. Higher density = more information per token spent.                                                                       |
| **Recency**               | Low-Medium | How recently the file/symbol was modified (git log). Weights more recent code as more likely relevant for active development tasks. Configurable — disabled for stable/historical queries.         |
| **Role alignment**        | Medium     | How well the item's kind matches the role's extraction profile. A builder cares about implementation, so function bodies rank higher. A reviewer cares about contracts, so interfaces rank higher. |
| **Discovery agreement**   | Low        | How many discovery methods independently found this candidate. Found by LSP and text search = higher confidence than text search alone.                                                            |

**Score computation:**

```
score = Σ (signal_value × signal_weight)
```

Weights are defined in configuration and tunable per-source type. The defaults above are starting points. The self-healing loop can adjust weights based on downstream acceptance rates — if study output for reviewer roles consistently gets rejected or heavily modified, the role alignment weights may be miscalibrated.

**Tie-breaking:** When scores are equal, prefer: smaller token count (more budget-efficient), then closer to entry point, then alphabetical by path (deterministic ordering).

Items are sorted by descending score. The ranked list passes to compression.

### Stage 4: Compress

Compression answers: **how do I fit the most relevant content into the token budget?** This is where the layered approach matters most.

**The budget model:**

```
available = budget - overhead
overhead  = framing tokens (source metadata, section headers, instructions for downstream AI)
```

The pipe reserves ~5-10% of the budget for framing — the metadata that tells the downstream consumer what it's looking at. The rest is available for content.

**Compression tiers (deterministic first, AI last):**

```
Tier 0: No compression
    Fill the budget greedily from the ranked list.
    Take the highest-ranked item, add it, subtract its token count.
    Repeat until the next item would exceed remaining budget.
    │
    ├── Budget met? → Done. Output.
    │
    ▼ (significant relevant content remains outside budget)

Tier 1: Granularity demotion
    For items already included, demote granularity one level:
    declaration → signature + docstring
    interface → signature list
    block → first/last N lines with "..." indicator
    │
    This frees budget for more items from the ranked list.
    Re-run greedy fill with the freed space.
    │
    ├── Budget met with acceptable coverage? → Done. Output.
    │
    ▼ (still too much relevant content for the budget)

Tier 2: Structural elision
    Remove content that is inferrable from context:
    - Strip function bodies when signature + docstring suffices
    - Collapse import blocks to "imports: [list of packages]"
    - Replace repetitive patterns with "N similar methods omitted"
    - Remove comments (preserve docstrings)
    - Collapse consecutive similar items ("5 similar getter methods")
    │
    ├── Budget met? → Done. Output.
    │
    ▼ (budget still tight, many relevant items excluded)

Tier 3: AI summarization (non-deterministic, optional)
    Group remaining excluded-but-relevant items by theme.
    For each group, generate a dense summary paragraph:
    "The auth package contains 12 functions handling OAuth2 flow.
     Key types: TokenStore (interface, 4 methods), Session (struct,
     7 fields including refresh_token and expiry). Entry points:
     Authenticate(), RefreshToken(), ValidateSession()."
    │
    These summaries replace the individual items they cover.
    Append summaries to fill remaining budget.
    │
    → Done. Output.
```

**The critical property:** Tiers 0-2 are fully deterministic. They are fast, predictable, and token-cost-free. Most context assembly completes at Tier 0 or Tier 1. AI (Tier 3) only fires when the query touches a large, interconnected area of the codebase AND the budget is tight relative to the relevance spread.

**Token counting:** The pipe uses a fast tokenizer (tiktoken or equivalent) to count tokens precisely, not estimate. Estimation errors compound across dozens of items and can blow the budget or waste 20% of it. Count once, allocate precisely.

**The `--compression` flag** caps the maximum tier. `--compression=none` stops at Tier 0 (greedy fill only). `--compression=structural` stops at Tier 2. `--compression=ai` (default) allows all tiers. This lets the planner or user control whether AI is invoked — useful when latency matters more than coverage, or when the downstream pipe wants raw content without summarization.

**Coverage tracking:** The pipe tracks how much of the relevant content (by score) was included vs excluded. This is reported in the output metadata:

```
coverage:
  included_items: 18
  excluded_items: 7
  included_score_mass: 0.82      # 82% of total relevance score included
  compression_tier_used: 1       # highest tier that was needed
  budget_used: 7,642             # tokens used
  budget_total: 8,000            # total budget
  ai_summaries: 0                # number of AI summary groups generated
```

High coverage with low compression tier = the budget was adequate. Low coverage even with high compression tier = the budget is too tight for this query's scope, or the entry point is too broad. This signal feeds the self-healing loop.

---

## Output Envelope

The study pipe produces a structured envelope designed for immediate consumption by downstream pipes (especially non-deterministic ones that need context for their prompts).

```yaml
pipe: study
action: gather
args:
  { source: codebase, role: builder, budget: "8000", entry: "internal/auth" }
timestamp: 2026-01-15T10:30:00Z
duration: 450000000 # 450ms
content_type: structured
error: null
content:
  summary: |
    Context gathered from codebase for builder role.
    Entry point: internal/auth/. 18 items included across 6 files.
    Coverage: 82% of relevance score within budget.

  items:
    - path: internal/auth/service.go
      symbol: AuthService
      kind: interface
      granularity: interface
      distance: 0
      tokens: 120
      content: |
        // AuthService handles authentication and session management.
        type AuthService interface {
            Authenticate(ctx context.Context, creds Credentials) (*Session, error)
            RefreshToken(ctx context.Context, sessionID string) (*Token, error)
            ValidateSession(ctx context.Context, token string) (*Session, error)
            Revoke(ctx context.Context, sessionID string) error
        }

    - path: internal/auth/session.go
      symbol: Session
      kind: type
      granularity: declaration
      distance: 0
      tokens: 85
      content: |
        // Session represents an authenticated user session.
        type Session struct {
            ID           string
            UserID       string
            Token        Token
            RefreshToken string
            ExpiresAt    time.Time
            CreatedAt    time.Time
            Metadata     map[string]string
        }

    # ... more items, ordered by relevance score descending

  ai_summaries: []
    # Populated only when Tier 3 fires:
    # - theme: "OAuth2 token lifecycle helpers"
    #   covers: [internal/auth/token_helpers.go, internal/auth/token_store.go]
    #   tokens: 150
    #   content: "The token lifecycle is managed through..."

  metadata:
    source: codebase
    entry: internal/auth
    role: builder
    depth: normal
    coverage:
      included_items: 18
      excluded_items: 7
      included_score_mass: 0.82
      compression_tier_used: 1
      budget_used: 7642
      budget_total: 8000
      ai_summaries: 0
    discovery:
      lsp_candidates: 31
      index_candidates: 8
      text_candidates: 14
      deduplicated_total: 38
    timing:
      discover_ms: 120
      extract_ms: 180
      rank_ms: 15
      compress_ms: 135
```

The `items` array is ordered by relevance score. Each item is self-describing — a downstream AI pipe receives items with paths, symbols, kinds, and content. The downstream pipe's prompt can reference "the auth interface" and the model knows exactly what it's looking at.

The `summary` field gives a one-paragraph overview suitable for voice output or quick inspection.

The `metadata` block gives the runtime and the self-healing loop everything needed to evaluate this invocation's quality.

---

## Codebase Backend: LSP Integration

The codebase backend is the most complex source type. It warrants specific discussion because the LSP integration is what differentiates this pipe from "grep and pray."

### LSP Session Management

The study pipe does not start or manage LSP servers. The Virgil runtime manages LSP sessions as long-lived background processes — one per language per workspace. The study pipe connects to an already-running LSP session via the runtime's LSP broker.

**Why the runtime manages LSP:** LSP servers are stateful — they index the workspace, build symbol tables, track file changes. Starting one per pipe invocation would be prohibitively slow (gopls initial indexing can take 30+ seconds on a large repo). The runtime starts LSP servers at workspace registration time and keeps them warm.

**What the pipe does:**

1. Receives an LSP client handle from the runtime (via environment variable or protocol extension)
2. Sends LSP requests (workspace/symbol, textDocument/references, etc.)
3. Processes responses into candidates
4. Does not manage LSP lifecycle, initialization, or file watching

### LSP Request Strategy

Given an entry point, the pipe builds a discovery plan:

**Entry point is a file path:**

```
1. textDocument/documentSymbol → get all symbols in the file
2. For each exported symbol:
   a. textDocument/references → find all usage sites
   b. textDocument/typeDefinition → find type definitions
3. For each usage site (up to depth limit):
   a. textDocument/documentSymbol → get the containing symbol
   b. callHierarchy/outgoingCalls → find what it calls
```

**Entry point is a symbol name:**

```
1. workspace/symbol → find the symbol definition(s)
2. textDocument/definition → resolve to location
3. textDocument/references → find all usages
4. callHierarchy/incomingCalls → who calls this?
5. callHierarchy/outgoingCalls → what does this call?
6. textDocument/typeDefinition → resolve types used
7. textDocument/implementation → find implementations (if interface)
```

**Entry point is a query string (no specific file/symbol):**

```
1. workspace/symbol → fuzzy match against all symbols
2. Parallel: text search for the query string
3. Take the top N matches, treat each as a symbol entry point
4. Follow references and call graph from each
```

**Depth control:**

| Depth   | Reference hops | Call graph hops | Typical candidates |
| ------- | -------------- | --------------- | ------------------ |
| shallow | 1              | 0               | 10-30              |
| normal  | 2              | 1               | 30-80              |
| deep    | 3              | 2               | 80-200+            |

Each hop multiplies candidates, so the depth flag has a dramatic effect on discovery breadth and timing. The default (normal) balances coverage with speed.

### Tree-Sitter Extraction

Once candidates are identified by location (file + line range), tree-sitter parses the file and extracts the AST node at the right granularity.

**Why tree-sitter over regex/line-based extraction:**

- **Accurate boundaries.** A function in Go might start on line 45 and end on line 120. Regex can find the `func` keyword but can't reliably find the closing brace (nested braces, string literals, etc.). Tree-sitter gives you the exact node boundaries.

- **Granularity control.** Extracting just a function signature means taking the tree-sitter node for the function declaration and stripping the body node. This is a tree operation, not a text operation. No regex can reliably extract "everything except the body" across languages.

- **Language-agnostic.** Tree-sitter grammars exist for every language Virgil might encounter. The extraction logic is written against tree-sitter node types, not language-specific regex patterns. Adding a new language means adding a grammar, not rewriting extraction rules.

**Extraction by node type:**

```
function_declaration → {
  signature:   parameter list + return type + docstring
  declaration: signature + body
}

type_declaration → {
  interface:   name + method signatures
  declaration: name + all fields + methods
}

method_declaration → {
  signature:   receiver + parameter list + return type + docstring
  declaration: signature + body
}

variable_declaration → {
  signature:   name + type
  declaration: name + type + initializer
}
```

### Fallback When LSP Is Unavailable

Not every workspace has an LSP server running. Not every language has good LSP support. The study pipe must degrade gracefully:

1. **LSP available:** Full semantic discovery + tree-sitter extraction. Best quality.
2. **Tree-sitter only (no LSP):** Parse files for structural discovery. No reference/call graph, but accurate extraction. Discover via structural index + text search only.
3. **Neither available:** Fall back to text search + line-based extraction. Discover via ripgrep/FTS5. Extract via line ranges with heuristic boundary detection. This is the "grep and pray" baseline — still better than reading whole files, but without structural awareness.

The output metadata reports which backends were available so the downstream consumer (and the self-healing loop) knows the quality of the context.

---

## Memory Backend

The memory source is simpler than codebase because Virgil's memory system already handles relevance-based retrieval (see `ARCHITECTURE.md` decisions #22, #24-26).

**Discovery:** Query the memory store with the topic/query. The memory system returns candidates ranked by its own relevance scoring (topic match, recency, tags).

**Extraction:** Memory entries are already stored as discrete items. Extraction is just retrieving the content. Granularity control applies to summaries vs. raw entries — the study pipe can read weekly summaries for broad context or raw entries for specific detail.

**Ranking:** Memory's own ranking is the primary signal. The study pipe may re-rank based on role alignment (a builder might prefer technical memory entries over meeting notes, even if the meeting notes are more recent).

**Compression:** Memory entries are typically short. Tier 0 (greedy fill) usually suffices. Tier 2 (elision) might collapse many small entries into a grouped summary. Tier 3 is rare for memory.

---

## Token Counting

The pipe uses a deterministic token counter. Not an estimate — an actual count.

**Why precision matters:** With 30+ items competing for a budget, estimation errors of even 10% per item compound into a 300-token variance. That's either a blown budget (bad: downstream pipe gets truncated) or 300 wasted tokens (bad: that's another function signature that could have been included).

**Implementation:** Use a tiktoken-compatible tokenizer in Go. The tokenizer must match the encoding used by the downstream AI model. Since the provider is configured at the server level, the study pipe reads the provider configuration and selects the matching tokenizer.

**Where counting happens:**

1. After extraction — each extracted item gets an actual token count
2. During compression — the running total is precise, not estimated
3. In the output metadata — `budget_used` is exact

If the downstream model's tokenizer isn't available (unusual provider, custom model), fall back to a conservative estimate (character count / 3.5 for English, / 2.5 for code). Report estimation in metadata so the self-healing loop knows the count isn't precise.

---

## Performance

The study pipe is on the critical path for every non-trivial pipeline. If it's slow, everything downstream waits.

**Latency targets:**

| Source                  | Target  | Breakdown                                                              |
| ----------------------- | ------- | ---------------------------------------------------------------------- |
| Codebase (warm LSP)     | < 500ms | 150ms discover + 200ms extract + 20ms rank + 100ms compress (Tier 0-2) |
| Codebase (cold, no LSP) | < 2s    | Text search is slower than LSP semantic queries                        |
| Memory                  | < 100ms | Memory store is local SQLite, already fast                             |
| Files                   | < 300ms | Filesystem search + text extraction                                    |
| Codebase + AI compress  | < 3s    | Tier 3 adds an AI call, unavoidably slower                             |

**What makes it fast:**

- LSP requests are concurrent. The pipe fires multiple LSP requests in parallel (references, call graph, type definitions) and merges results.
- Tree-sitter parsing is incremental. On warm files (already parsed), extraction is near-instant.
- Ranking is pure arithmetic. No allocations in the scoring loop.
- Tier 0-2 compression is deterministic string operations. No network calls.

**What makes it slow (and how to avoid it):**

- **Cold LSP.** If the LSP server hasn't indexed the workspace yet, symbol queries block on indexing. Mitigation: the runtime starts LSP servers eagerly at workspace registration. By the time the user asks a question, the index is warm.
- **Deep discovery on a large codebase.** `--depth=deep` on a highly-connected codebase can produce 200+ candidates. Mitigation: candidate limits per discovery method (configurable, default 50 per method, 100 total).
- **Tier 3 AI compression.** An AI call is 1-3 seconds. Mitigation: only fire when Tiers 0-2 leave significant relevant content outside the budget AND `--compression=ai` is set.

---

## Self-Healing Signals

The study pipe emits signals that feed the generalized improvement loop:

**Coverage vs. budget:** If coverage is consistently low (< 60% score mass included) for a source type, the budget may be too small or the discovery is too broad. The improvement pipeline can propose default budget increases or entry point narrowing heuristics.

**Compression tier frequency:** If Tier 3 fires on > 20% of invocations, the structural compression (Tiers 1-2) may be missing opportunities. The improvement pipeline can analyze which items are being AI-summarized and propose new structural elision rules.

**Downstream acceptance:** If the downstream pipe (e.g., `draft`, `build`) consistently fails or requires retries, the context quality may be insufficient. The improvement pipeline correlates study output with downstream outcomes to identify what's missing.

**Discovery method contribution:** Track which discovery methods (LSP, structural index, text search) contribute items that actually make it into the final output vs. being filtered by ranking. If text search rarely contributes surviving items, its weight can be reduced to save discovery time.

---

## Planned Metrics

```yaml
metrics:
  - name: coverage_rate
    type: average
    field: coverage.included_score_mass
    window: 7d
    threshold:
      warn: 0.65
      degrade: 0.50

  - name: tier3_frequency
    type: ratio
    numerator: invocations_using_tier3
    denominator: total_invocations
    window: 7d
    threshold:
      warn: 0.30
      degrade: 0.50

  - name: p95_latency
    type: percentile
    field: duration
    percentile: 95
    window: 7d
    threshold:
      warn: 1000000000 # 1s
      degrade: 3000000000 # 3s

  - name: downstream_success
    type: ratio
    numerator: downstream_accept
    denominator: downstream_total
    window: 14d
    threshold:
      warn: 0.75
      degrade: 0.60
```

---

## Prompts (Tier 3 Only)

The study pipe is primarily deterministic. It only uses AI for Tier 3 compression. The prompts are tightly constrained — the AI is summarizing, not reasoning.

```yaml
prompts:
  system: |
    You are a technical summarizer. You compress code and technical
    content into dense, information-rich summaries. You preserve
    type names, function signatures, key constants, and architectural
    relationships. You never invent information not present in the source.
    You optimize for maximum information per token.

  templates:
    summarize_group: |
      Summarize the following {{.ItemCount}} code items into a dense
      paragraph of approximately {{.TargetTokens}} tokens.

      Preserve: type names, function signatures, interface definitions,
      key constants, error types, and dependency relationships.

      Remove: implementation details, comments, formatting, boilerplate.

      Items:
      {{range .Items}}
      --- {{.Path}} ({{.Symbol}}, {{.Kind}}) ---
      {{.Content}}
      {{end}}

      Produce a single dense paragraph. No headers, no bullets, no code blocks.
      Just information-dense prose that a developer can read to understand
      what these components do and how they relate.
```

The prompt deliberately constrains the AI to summarization, not interpretation. It must preserve names and signatures (which downstream AI needs for accurate code generation) and strip implementation detail (which is the lowest-density content).

---

## Subprocess Entry Point

```go
func main() {
    // Study is hybrid: deterministic by default, non-deterministic only for Tier 3
    provider, providerErr := pipehost.BuildProviderFromEnv()
    // Provider failure is NOT fatal — Tiers 0-2 still work
    if providerErr != nil {
        fmt.Fprintf(os.Stderr, "study: AI provider unavailable, Tier 3 compression disabled: %v\n", providerErr)
    }

    dbPath := os.Getenv(pipehost.EnvDBPath)
    lspBroker := lsp.NewBrokerFromEnv() // connects to runtime's LSP session manager

    pc, err := pipehost.LoadPipeConfig()
    if err != nil {
        pipehost.Fatal("study", err.Error())
    }

    handler := study.NewHandler(study.Config{
        Provider:  provider,    // may be nil — Tier 3 disabled
        DB:        dbPath,
        LSP:       lspBroker,
        PipeConfig: pc,
    })

    pipehost.Run(handler, nil) // no streaming — study returns a complete result
}
```

Note: the provider is optional. If the AI provider is unavailable, the pipe works fine — it just caps compression at Tier 2. This is the deterministic-first philosophy in practice: the pipe degrades to structural compression, not to failure.

---

## Open Questions

### LSP broker protocol

The runtime needs to manage LSP sessions and expose them to subprocess pipes. The mechanism isn't defined yet. Options:

1. **Unix socket per LSP server.** The runtime starts LSP servers that listen on sockets. The study pipe connects directly. Simple but requires socket management.
2. **Proxy through the runtime.** The study pipe sends LSP requests to the runtime via a dedicated channel. The runtime forwards to the LSP server. Adds latency but centralizes management.
3. **Embedded LSP client in the runtime.** The runtime makes LSP calls on behalf of the pipe and passes results through the envelope. Cleanest interface but requires the runtime to understand LSP request patterns.

**Leaning:** Option 1 for v0.1.0. Direct socket is the simplest thing that works. The runtime passes the socket path via environment variable. The pipe connects and sends standard LSP JSON-RPC.

### Cross-language discovery

A Go file calls a Python service via HTTP. An LSP server for Go doesn't know about the Python service's API. Discovery stops at the HTTP call boundary.

**Mitigations:**

- The structural index can link files across languages via configuration references, shared types, or naming conventions.
- Text search catches cross-language references by string.
- Future: API schema files (OpenAPI, protobuf) as cross-language bridges. The study pipe could parse these to connect call sites to service definitions across language boundaries.

### Workspace registration

The study pipe assumes a workspace is registered with the runtime. The registration process (which directories constitute a workspace, which languages have LSP servers, when to start/stop servers) is a runtime concern not yet defined.

### Incremental indexing

The structural index and text index need to stay current as files change. The runtime should watch for file changes and update indexes incrementally. The study pipe always reads the current index — it doesn't manage index freshness.
