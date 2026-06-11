---
name: email-service-pr-readiness
description: >
  Repo-local pre-PR shape check for lfx-v2-email-service. Audits only branch
  and PR hygiene: branch name, JIRA reference, conventional commits, rebase
  status, DCO plus GPG signing, total diff size, and email-service protected
  files. Does not audit Go code or architecture. Run before
  /email-service-preflight.
context: fork
allowed-tools: Bash, Read, Glob, Grep
---

<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Email Service PR Readiness

Check whether local commits are shaped correctly to open as a PR for
`lfx-v2-email-service`.

This skill does not audit code, NATS behavior, SMTP/SES/SQS tracking logic, or
Helm correctness. It only checks branch and PR hygiene. Run
`/email-service-preflight` after this passes.

Output a structured shape report with verdict `NOT READY`, `READY WITH CHANGES`,
or `READY`. Do not mutate the working tree or index, and do not create a PR.

## Phase 1: Parse Arguments

Args format: `[base-branch] [extra instructions]`.

- First token, if it looks like a ref or branch name, is the base branch.
- Default base: `origin/main`.
- If the base has no `/`, normalize it to `origin/<base>`.
- Treat remaining text as context only. Do not expand scope beyond PR shape.

## Phase 2: Gather Inputs

Run from the repository root:

```bash
git fetch origin
git rev-parse --abbrev-ref HEAD
git diff --shortstat <base>...HEAD
git diff --name-only <base>...HEAD
git log --format='%H %s' <base>..HEAD
git log --format='%G? %h %s' <base>..HEAD
git log --format=%B <base>..HEAD
git merge-base --is-ancestor <base> HEAD; echo $?
```

If there are no commits between `<base>` and `HEAD`, abort with:

```text
No commits to audit against <base> - make at least one commit on this branch.
```

## Phase 3: Protected Files

Build the protected-file finding by intersecting `git diff --name-only
<base>...HEAD` with this repo-specific list. Do not use central or generic
protected-file lists.

Protected paths:

- `pkg/api/**` - public Go contract for subjects, payloads, responses, and KV bucket constants.
- `pkg/api/nats.go` - NATS subject and payload contract file.
- `cmd/email-service/main.go` - NATS subscription wiring and SQS poller startup.
- `cmd/email-service/config.go` - SMTP, SES, SQS, NATS, and health probe config.
- `internal/service/*handler*.go` - NATS request/reply handlers.
- `internal/service/engagement_event_handler.go` - SES engagement event handling and KV tracking updates.
- `internal/infrastructure/smtp/**` - SMTP and SES mail assembly boundary.
- `internal/infrastructure/sqs/**` - SQS engagement event polling.
- `charts/lfx-v2-email-service/**` - service-local deployment chart.
- `go.mod`
- `go.sum`
- `Makefile`
- `CLAUDE.md`
- `.claude/skills/**`
- `docs/email-service-contract.md`
- `docs/email-engagement-tracking.md`
- `docs/service-helm-chart.md`

Protected-file changes are not automatically blockers. They are PR-shape
signals: surface them in the PR body and tag the relevant owner or reviewer.

## Phase 4: Shape Checks

Evaluate only these items:

- Branch name: should include `LFXV2-<number>` or be an explicit maintenance branch.
- JIRA reference: at least one `LFXV2-<number>` should appear in commit subjects or bodies unless the work is explicitly ticketless maintenance.
- Conventional commits: each commit subject should match `type(scope): summary` or `type: summary`.
- Rebase status: `git merge-base --is-ancestor <base> HEAD` exit code `0` means `<base>` is an ancestor of the branch.
- DCO: every commit body should contain a `Signed-off-by:` trailer.
- GPG signing: every commit should have acceptable `%G?` status, normally `G`.
- Diff size: flag large branches for PR splitting or explicit PR context.
- Protected files: report every protected path touched.

Use `CRITICAL` only for shape that blocks opening a reviewable PR:

- No commits against base.
- Branch cannot be compared to base.
- Any commit missing DCO signoff.
- Any commit with a bad or missing GPG signature.
- Commit subjects are not conventional and cannot be explained as merge/revert commits.

Use `SHOULD_FIX` for review friction:

- Branch name or commit messages lack a JIRA reference.
- Branch is not rebased on `<base>`.
- Diff is large enough to need splitting or explicit reviewer context.
- Protected files are touched without a clear PR note.

Use `NIT` for minor naming or wording issues.

## Phase 5: Render Report

```markdown
# Email Service PR Readiness

**Branch:** `<current-branch>` -> `<base>`
**Commits:** N | **Additions:** +A | **Deletions:** -D
**Verdict:** NOT READY | READY WITH CHANGES | READY

## PR-shape sanity

| Check | Status | Detail |
| --- | --- | --- |
| Branch name | PASS | feat/LFXV2-1234-email-tracking |
| JIRA ticket | PASS | Found LFXV2-1234 in commits |
| Conventional commits | PASS | 3/3 commit subjects valid |
| Branch rebased | PASS | origin/main is an ancestor |
| Diff size | SHOULD_FIX | 1200 additions; explain scope in PR body |
| DCO + GPG signing | PASS | 3/3 commits signed and signed off |
| Protected files | SHOULD_FIX | pkg/api/nats.go, charts/lfx-v2-email-service/values.yaml |

## Findings

- `severity`: rule id, message, suggestion.
```

Verdict rules:

- `NOT READY`: any `CRITICAL` finding.
- `READY WITH CHANGES`: no `CRITICAL`; at least one `SHOULD_FIX`.
- `READY`: no `CRITICAL` or `SHOULD_FIX`.

## Companion Skill

Run `/email-service-preflight` after this report is `READY` or the remaining
`READY WITH CHANGES` items have been intentionally documented for the PR.
