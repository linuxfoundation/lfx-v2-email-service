<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Email Service Review Knowledge Base

Empirical review-pattern knowledge base for `lfx-v2-email-service`. Each pattern was extracted
from a real review comment on a merged PR in this repo and cited with `PR #N file:line` + a
quote from the actual comment. One exception: `known-false-positives.md` may also carry dated
entries carried over from the retired repo conventions reviewer (`email-service-code-reviewer`,
retired 2026-09-25), each marked as such in its **Source** line instead of a PR citation.

> **STARTER KB.** This repo had only **8 merged PRs** at the time of authoring (all by a single
> author, reviewed primarily by GitHub Copilot plus two human reviewers on PR #1). Per the
> thin-corpus section of the service KB research playbook, recurrence across ≥2 PRs is rarely
> reachable here, so most entries are promoted on **cost-of-miss** (security / data-integrity /
> crash / contract) and **acted-on-by-maintainer** signal. This is a small, sharp starting set;
> it is **expected to grow** as PR history accumulates. Re-run the playbook research pass after
> the next ~10–15 merged PRs and promote newly-recurring patterns.

## What this KB is (and is not)

This KB is the **empirical** review surface — patterns that bots and human reviewers have
actually flagged on this repo's PRs. It does **not** duplicate the other two reviewers of the
pre-PR round:

- `/lfx-skills:lfx-general-code-review` — generic correctness / quality / test intuition, plus
  the documented rule surface (CLAUDE.md, the `email-service-dev` skill, contract docs, chart
  docs).
- `/lfx-skills:lfx-security-engineer` — OWASP-class security review.

It is consumed only by the `/email-service-learnings-reviewer` skill, which reviews the pinned
range `git diff <base_sha> <target_sha>` (`base_sha` = merge-base with the PR base,
`target_sha` = branch head), routes to the category files below by changed-file path, matches
each entry's `Detect:` rule, and emits only findings it can quote from an entry (KB-match gate).
`known-false-positives.md` is applied last as the floor filter on that reviewer's findings; the
general and security reviewers do not read this KB, so it never filters their findings.

## Methodology

Built per `lfx-architecture-scratch/2026-05-DevX-Time-to-Merge/service-kb-research-playbook.md`.
Corpus: merged PRs only. For each PR, all three comment surfaces (inline review comments, review
bodies, issue/conversation comments) plus GraphQL review-thread resolution state were pulled via
the `gh` CLI, clustered into candidate patterns, and run through the promotion gate (A hard
gates: repo-specific, mechanically detectable, currently relevant, not tooling-enforced; B value
signal: recurrence / cost-of-miss / acted-on authority). The single non-corpus source is the
carry-over described above: standing suppressions salvaged from the retired conventions reviewer
into `known-false-positives.md`, dated and marked as carried over.

## Corpus stats

- **Merged PRs sampled:** 8 (#1–#8), all merged 2026-05-14 … 2026-05-27, single author
  (`andrest50`).
- **Review surfaces present:**
  - **GitHub Copilot** (`copilot-pull-request-reviewer[bot]` overviews + `Copilot` inline) —
    the dominant surface, ~55 distinct inline comments across the corpus.
  - **`github-license-compliance[bot]`** — 2 `go.mod` license alerts on PR #1 (dismissed as a
    known transitive dep).
  - **Human reviewers** — 3 inline comments on PR #1 (`mauriciozanettisalomao` ×2,
    `prabodhcs` ×1), all "not a blocker" nits deferred to follow-up PRs.
- **CodeRabbit:** **OFF.** Zero CodeRabbit comments on any surface across all 8 PRs (confirms
  the playbook's sampling note).
- **Acted-on signal:** nearly every Copilot inline thread was **resolved by a code change**;
  the handful of unresolved threads (PR #6 logging-volume nits) were explicitly declined and
  are recorded in `known-false-positives.md`.

## Categories

| File                            | Patterns | Read when                                                                                          |
| ------------------------------- | -------- | -------------------------------------------------------------------------------------------------- |
| `nats-handler-contract.md`      | 6        | `internal/service/**` or `pkg/api/**` changed (handler reply paths, error strings, public types).  |
| `tracking-kv-engagement.md`     | 9        | engagement handler, send-handler KV path, `internal/infrastructure/sqs/**`, tracking-ID / SES.     |
| `smtp-security-startup.md`      | 7        | `internal/infrastructure/smtp/**`, `pkg/redaction/**`, `cmd/email-service/**` (MIME, lifecycle).   |
| `docs-and-chart.md`             | 4        | `README.md` / `CLAUDE.md` / `docs/**` / `charts/lfx-v2-email-service/**` doc-vs-code / chart.       |
| `known-false-positives.md`      | 9        | always, applied LAST as the floor filter.                                                          |

**Total: 26 promoted patterns + 9 known-false-positive entries** (8 from the PR corpus, 1 carried
over from the retired conventions reviewer).

The pattern count sits above the playbook's "~8–15" thin-corpus expectation because PR #1
(initial service) and PR #4 (engagement tracking) were unusually large, contract-defining PRs
that drew dense, high-cost-of-miss review (header injection, KV concurrency, lost SQS messages,
shutdown safety). Most entries are single-occurrence-but-acted-on; few recur across PRs.

## Highest-value patterns

- `tracking-kv-engagement/blind-put-no-optimistic-lock` (Critical) — concurrent KV updates lose
  engagement increments; recurred within PR #4 and is the core data-integrity rule.
- `tracking-kv-engagement/lost-sqs-message-on-kv-failure` (Critical) — returning `nil` after a
  failed KV write silently drops engagement events.
- `nats-handler-contract/kv-error-treated-as-not-found` (Critical) — the most-recurring pattern
  (PR #4 ×2, PR #7), transient store errors masquerading as "not found".
- `smtp-security-startup/header-injection` (Critical) — SMTP header injection via unsanitized
  `to` / `subject`, including the ASCII-subject `QEncoding` gap.

## Date

Authored 2026-05-29.
