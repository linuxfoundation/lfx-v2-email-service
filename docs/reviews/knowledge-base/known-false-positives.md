<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Known false positives — applied LAST in every review pass

Findings that match any pattern below MUST be dropped, regardless of which source (KB pattern
file, bot, human) originally produced them. This list is the floor — even a quotable KB pattern
does not survive if it matches a known false positive.

Used by the `lfx-skills:lfx-email-service-learnings-reviewer` subagent (Step 4). Each entry was
explicitly decided as noise-on-this-repo from the merged-PR corpus (PRs #1–#8), where the
maintainer either declined the suggestion or it conflicts with an intentional design choice
documented in the service docs.

---

## Tooling-enforced / out-of-scope-for-review

### Go formatting, lint, vet, license-header complaints on a compliant file

**Pattern matched:** any finding about gofmt/`gofmt -s` formatting, golangci-lint style, `go
vet`, or a missing two-line MIT license header on a file that has one.

**Why false:** these are enforced deterministically by `make fmt` / `make check` /
`make lint` and `/email-service-preflight`. Surfacing them in an empirical-pattern review is
duplicate signal. (Playbook A-gate: not already enforced by deterministic tooling.)

**Source:** `Makefile` (`fmt`, `lint`, `check`, `license-check` targets);
`.claude/skills/email-service-preflight/SKILL.md`.

### `github-license-compliance[bot]` BSD-3-Clause / google-patent-license alert on `go.mod`

**Pattern matched:** any finding quoting `BSD-3-Clause AND
LicenseRef-scancode-google-patent-license-golang` or the license-compliance bot's `go.mod`
alert.

**Why false:** this expression comes from Go's extended stdlib (`golang.org/x/crypto`,
`golang.org/x/sys`), required transitive deps of `nats.go`, and is shared by every LFX v2 NATS
service. The maintainer marked it "No action needed" on PR #1 (resolved without change).

**Source:** PR #1 `go.mod:1` — andrest50: "transitive dependency via `nats.go` ... cannot be swapped out ... No action needed."

### "Add Copilot custom instructions" promotional CTA

**Pattern matched:** the trailing "Add Copilot custom instructions for smarter, more guided
reviews" text Copilot appends to every PR overview.

**Why false:** promotional boilerplate, not a finding.

---

## Intentional design choices on this repo

### `open_count` field flagged as redundant vs `len(opened_at_list)`

**Pattern matched:** any finding stating `open_count` should be removed from
`EmailRecipientRecord` because it is derivable from `len(opened_at_list)`, or that it lacks
`omitempty`.

**Why false:** intentional. The maintainer keeps `open_count` stored and synced with
`len(opened_at_list)` after each append; replayed open events return early before the append so
the count is never inflated. (Note: a genuine *breaking removal/rename* of a `pkg/api` field is
still in scope — see `nats-handler-contract/pkg-api-breaking-field-removal`. This FP only covers
the "delete `open_count` as redundant" suggestion.)

**Source:** PR #8 `pkg/api/nats.go:75` — andrest50: "Intentional — `open_count` is stored and kept in sync with `len(opened_at_list)` immediately after each append."

### KV legacy `opened_at` backward-compat / migration demanded

**Pattern matched:** any finding demanding a migration/backfill path for legacy `opened_at` KV
records when the open-tracking shape changes.

**Why false:** `SES_EVENTING_ENABLED` was `false` in all environments until the companion
ArgoCD rollout, so the engagement handler had never run and no KV record contains a legacy
`opened_at` value. Send-time records have `opened=false` and unmarshal cleanly into the new
shape.

**Source:** PR #8 `pkg/api/nats.go:77` — andrest50: "No migration needed in practice ... the engagement event handler has never run and no KV records contain a legacy `opened_at` value."

### `Opened` boolean vs precise open counter (when the ask is "must be a counter")

**Pattern matched:** finding stating the record must store a precise open *count* rather than a
boolean, framed as a correctness bug.

**Why false (conditional):** the design deliberately exposes "was this opened?" for the known
callers (invite-service, committee-service); a boolean is idempotent on re-opens. The current
code does keep an `opened_at_list` + `open_count`, so this only applies to findings that treat
the *boolean Opened* flag as wrong. If a future caller genuinely needs counts and the code drops
them, the finding is valid — author's call.

**Source:** PR #4 `pkg/api/nats.go:73` — andrest50: "Intentional — callers only need 'was this email opened?', not a precise count. The boolean is idempotent on re-opens."

### `net/smtp` send not cancellable on context timeout

**Pattern matched:** finding that `smtp.SendMail` run in a goroutine does not actually abort the
TCP send on `ctx` cancellation (risk of false-negative response / duplicate send).

**Why false (acknowledged limitation):** `net/smtp` has no context-aware dial/abort API. The
maintainer accepted this as a known Phase-1 limitation bounded by the SMTP timeout; a fully
cancellable client is explicitly out of scope. Re-raise only if the repo adopts a
`net.DialContext`-based SMTP client and the cancellation path regresses.

**Source:** PR #1 `internal/infrastructure/smtp/message.go:86` — andrest50: "the goroutine approach unblocks the caller on ctx cancellation but the underlying TCP connection continues ... known limitation of `net/smtp` ... out of scope for Phase 1."

### Per-message INFO logging in the SQS poller / engagement handler flagged as too chatty

**Pattern matched:** finding that per-SQS-message or per-engagement-event INFO logs should be
downgraded to DEBUG or sampled to reduce volume.

**Why false:** the maintainer reviewed the volume and explicitly kept these at INFO ("It's not
very high volume, this is fine."). These threads were left unresolved by choice on PR #6.

**Source:** PR #6 `internal/infrastructure/sqs/poller.go:94` and `engagement_event_handler.go:104,136` — andrest50: "It's not very high volume, this is fine." / "This is fine."

---

## How to add a new entry

When you encounter a finding from Copilot (or a human reviewer) the team has explicitly decided
is not relevant for this repo:

1. Add an entry here with **Pattern matched**, **Why false**, and a **Source** (PR #N + quote).
2. If the pattern was previously in a category `.md`, remove it there — do not keep a pattern in
   both files.
3. If it is something the bots will surface forever (e.g. the license-compliance `go.mod`
   alert), it is permanent. One-time misreads do not need an entry.

This file should accumulate slowly. CodeRabbit is not currently enabled on this repo, so the
bot surface is Copilot + `github-license-compliance[bot]` only.
