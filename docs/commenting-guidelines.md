# Commenting Guidelines

This project prefers sparse, high-signal comments. Comments should explain intent, invariants, and fallback strategy, not restate obvious code.

## Where comments are required

- State machine transitions with non-obvious behavior.
- Fallback logic across external systems (`kubectl` vs `gh`).
- Data normalization or matching heuristics that can cause false positives/negatives.
- Config precedence and merge rules.
- Reliability logic (retry/backoff/idempotency expectations).

## Where comments should be avoided

- Straightforward assignments, loops, and if-conditions that are self-explanatory.
- Public API docs that only repeat function names.

## Style rules

- Keep comments in English and concise (1-3 lines).
- Focus on *why* and *constraints*.
- Place comments immediately above the affected block.
- Keep comments stable over time; avoid runtime details that drift quickly.

## Recommended structure by module

- `cmd/porch/watch.go`: comment transition and fallback branches.
- `pkg/component/doc.go`: comment check-run selection and run name normalization.
- `pkg/config/loader.go`: comment config source precedence and branch expansion.
- `pkg/retrier/retrier.go`: comment backoff bounds and rediscovery assumptions.
- `pkg/resolver/dag.go`: comment dependency graph validation and cycle detection.
