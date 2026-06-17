---
name: email-service-preflight
description: >
  Repo-local mechanical pre-PR pipeline for lfx-v2-email-service. Runs Go
  working-tree checks, license headers, formatting, lint, build, tests,
  protected-file reporting, commit verification, and PR change summary. Run
  after /email-service-pr-readiness.
allowed-tools: Bash, Read, Glob, Grep, Edit, Write, AskUserQuestion
---

<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Email Service Preflight

Run the mechanical pre-PR pipeline for `lfx-v2-email-service`. This skill is
email-service-specific and Go-specific. It does not perform broad code review,
architecture review, or central LFX workflow routing.

Run this after `/email-service-pr-readiness` has passed or after its remaining
shape warnings have been intentionally accepted for the PR.

## Modes

Args format: `[base-branch] [--dry-run|report only] [extra instructions]`.

- Default base: `origin/main`.
- If the base has no `/`, normalize it to `origin/<base>`.
- Default mode may run safe mechanical fixers such as `make fmt`.
- `--dry-run` or `report only` mode must not edit files. Use check-only commands
  such as `gofmt -l` and report what would change.
- If a command requires a slow external service, Docker, or credentials, stop and
  ask before running it. The normal Go checks below do not require those.

## Check 0: Working Tree Status

Run:

```bash
git status --short
git diff --stat <base>...HEAD
git log --format="%h %s%n%b" <base>..HEAD
```

Evaluate:

- Uncommitted changes: identify them before running formatters. Ask whether to
  continue if the changes look unrelated to the PR.
- No commits ahead of `<base>`: report that there is no branch diff to validate.
- Commit messages missing `LFXV2-`: flag for the PR readiness follow-up.
- Commits missing `Signed-off-by:`: flag before continuing.

Do not revert or discard any changes.

## Check 1: License Headers

Go files must start with:

```go
// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT
```

Markdown files in this repo use:

```html
<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->
```

Run:

```bash
make license-check
```

Note: `make license-check` only validates Go (`*.go`) files. It does not check
Markdown or other file types — CI's separate License Header Check covers those.
When adding or editing Markdown, add the HTML-comment header manually.

In default mode, add the standard two-line header only when the file type and
placement are clear. In dry-run mode, report missing headers without editing.

## Check 2: Formatting

Default mode:

```bash
make fmt
```

Dry-run/report-only mode:

```bash
gofmt -l $(find . -name '*.go' -not -path './vendor/*')
```

If default formatting changes files, list them from `git status --short`.

## Check 3: Lint

Run:

```bash
make lint
```

This repo expects `golangci-lint run ./...`. If `golangci-lint` is missing,
report the missing tool and do not replace it with a weaker lint result.

## Check 4: Build

Run:

```bash
make build
```

If build output changes `bin/`, confirm it remains ignored. If the build fails,
report the failing package, file, and line where available.

## Check 5: Tests

Run:

```bash
make test
```

`make test` runs `go test -race -timeout 5m ./...`. Treat test failures as
preflight failures and report the failing package and test names.

## Check 6: Protected Files

Get changed files:

```bash
git diff --name-only <base>...HEAD
```

Intersect the result with this repo-specific protected list. Do not use central
or generic protected-file lists.

Protected paths:

- `pkg/api/**` - public Go contract for NATS subjects, payloads, responses, and KV bucket constants.
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

Report each protected file touched with the reason. Protected files may be
valid changes, but the PR description must call them out.

## Check 7: Commit Verification

Run:

```bash
git status --short
git log --format="%h %s%n%b" <base>..HEAD
git log --format='%G? %h %s' <base>..HEAD
```

Verify:

- All intended changes are committed, or explicitly report uncommitted files.
- Commit subjects follow conventional commit format.
- Every commit has `Signed-off-by:`.
- Every commit has an acceptable GPG signature status.
- At least one JIRA reference appears in branch name, commit subject, or commit body unless the work is documented as ticketless maintenance.

## Check 8: Change Summary

Run:

```bash
git diff --stat <base>...HEAD
git diff --name-status <base>...HEAD
```

Summarize:

- New files created and their purpose.
- Modified Go packages and what changed.
- Public `pkg/api` contract changes.
- NATS subject, payload, response, or queue behavior changes.
- SMTP, SES, SQS, or KV engagement tracking changes.
- Helm chart changes.
- Docs updated with behavior or contract changes.
- Protected files touched.

## Results Report

Use this shape:

```text
EMAIL SERVICE PREFLIGHT RESULTS
--------------------------------
Working tree      PASS|FAIL|WARN - detail
License headers   PASS|FAIL|WARN - detail
Formatting        PASS|FAIL|WARN - detail
Lint              PASS|FAIL|WARN - detail
Build             PASS|FAIL|WARN - detail
Tests             PASS|FAIL|WARN - detail
Protected files   PASS|FAIL|WARN - detail
Commits           PASS|FAIL|WARN - detail
--------------------------------
READY FOR PR | READY WITH NOTES | ISSUES FOUND
```

Verdict rules:

- `ISSUES FOUND`: any failed license, lint, build, test, DCO, or GPG check.
- `READY WITH NOTES`: checks pass but uncommitted files, protected files, missing
  JIRA references, or dry-run-only findings remain to document.
- `READY FOR PR`: all checks pass with no remaining notes.

If default mode changed files, end by listing the modified files and asking the
contributor to review and commit them. If dry-run/report-only mode was used,
end with the exact checks that were skipped or not allowed to mutate files.
