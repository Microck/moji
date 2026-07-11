# Orchestration: Search and release reliability hardening

## Execution Rules

- Keep the original objective intact.
- Ask for approval before risky, expensive, external, or destructive actions.
- Keep immediate blocking work local.
- Delegate only bounded, disjoint, materially useful packets.
- Integrate packet results before final verification.

## Branching Rules

- If one candidate fails validation, record the failure and try the next
  ranked candidate; do not relax validation.
- If a failure is transient transport or server state, do not persist it as a
  negative content-health fact.
- If family fallback cannot remain atomic and predictable, fail before moving
  completed files into their final destinations.
- If source reliability conflicts with query relevance, relevance wins.
- If artifact versions disagree at any release stage, stop before publication.
- If the corpus exposes only a webpage or gated flow, classify it as a miss.

## Packet Prompts

1. Contracts: specify typed candidate failures, retryability, cache identity,
   source classes, and release gates before behavior changes.
2. Downloads: own app/download selection and integration tests; preserve all
   existing safety invariants.
3. URL health: own cache schema, TTL/size bounds, and invalid-result penalties.
4. Ranking: own source classification and precedence tests without changing
   legal trust semantics.
5. Release: own a TypeScript runbook/verifier, exact tarball inspection, and
   dry-run behavior; never publish during implementation.
6. Docs/CI: wire the shared verifier into CI and document user-visible behavior.
7. Audit: inspect the integrated diff, run all gates, and repeat the corpus.

## Completion Audit

Map every success criterion to current source, test, command, or corpus output.
Treat a green narrow test as insufficient proof for cross-platform release
behavior or full download semantics. Finish only when no required evidence is
missing and external publication has not occurred without approval.
