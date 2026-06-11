<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# NATS handler and public contract

Patterns for the NATS request/reply handlers and the public `pkg/api` wire contract. The
service guarantees every request path replies exactly once (so callers' `RequestWithContext`
never hangs), distinguishes "not found" from transient KV errors, returns caller-safe error
strings, and treats `pkg/api` as an import-stable public contract. These were the densest
empirical cluster in the corpus.

**Read when:** any file under `internal/service/**` or `pkg/api/**` changed, or the diff
touches a `Handle` / `HandleData` / `respond` / `Respond` / `replyError` path, a documented
error string, or an exported `pkg/api` type or field.

---

## `nats-handler-contract/kv-error-treated-as-not-found` — Critical

**Pattern:** a handler treats every KV `Get` error as "not found", so a transient JetStream
or network error returns a 404-style "not found" instead of surfacing as an internal error.
The caller cannot distinguish "no such record" from "the store is unavailable", and a
group-status loop silently undercounts on transient errors.

**Detect:** in `internal/service/*_handler.go`, find any `Get(...)` whose error branch returns
`"not found"` (or skips/continues) without an `errors.Is(err, natsgo.ErrKeyNotFound)` guard.
Every `Get` error path must branch on `ErrKeyNotFound` first.

**Empirical citation:** PR #4 `internal/service/get_email_status_handler.go:52` — Copilot — "treats any KV `Get` error as 'not found'. This will incorrectly return 404-style responses on transient JetStream/network errors. Distinguish `nats.ErrKeyNotFound` from other errors". Same flagged again PR #4 `get_email_engagement_analytics_handler.go:53` and PR #7 `get_email_status_handler.go:117` ("any KV Get error ... is currently treated as 'not found' and silently skipped"). All resolved by code change.

**Failure message:** KV `Get` error treated as "not found" without an `ErrKeyNotFound` guard — transient store errors will masquerade as missing records.

**Fix:** branch with `if errors.Is(err, natsgo.ErrKeyNotFound)` → respond `"not found"`; for any other error, `slog.ErrorContext` the error and respond `"internal error"`. In group loops, fail the whole request on a non-`ErrKeyNotFound` error rather than skipping the entry.

---

## `nats-handler-contract/respond-error-ignored` — Important

**Pattern:** the handler ignores the error returned by `msg.Respond` / the `respond` callback
on success and/or failure paths. If the reply fails (connection or drain issue), the caller
times out with no server-side signal.

**Detect:** in `internal/service/*_handler.go`, find `respond(` or `msg.Respond(` whose
return value is discarded (`_ =` or no assignment). Both the success path and `replyError`
must check and log the error.

**Empirical citation:** PR #1 `internal/service/handler.go:70` — Copilot — "Errors from respond/msg.Respond are ignored on both success and failure paths. If responding fails ... callers may time out without any server-side indication." Resolved: both paths now `slog.WarnContext` on a failed respond.

**Failure message:** `respond` / `msg.Respond` return value ignored — a failed reply will silently time out the caller.

**Fix:** capture the error and `slog.WarnContext(ctx, "failed to respond to NATS request", logging.ErrKey, err)` on every respond path (success and `replyError`).

---

## `nats-handler-contract/leaked-internal-error-to-caller` — Important

**Pattern:** a handler returns a raw `err.Error()` to the NATS caller, leaking internal
detail (SMTP host/port, parser internals) and making the reply contract unstable across
releases. The documented contract enumerates a fixed set of caller-safe error strings.

**Detect:** in `internal/service/*_handler.go`, find any `replyError(... err.Error())` or
`respond([]byte(err.Error()))`. Caller-facing errors must be one of the stable strings in
`docs/email-service-contract.md` (`"invalid request payload"`, `"email delivery failed"`,
`"not found"`, `"internal error"`, the `... is required` strings, etc.).

**Empirical citation:** PR #1 `internal/service/handler.go:61` — Copilot — "On send failure, the handler returns `err.Error()` directly to the NATS caller. This can leak internal implementation details ... and makes the API surface unstable. Consider mapping internal errors to a stable, user-safe error message while logging the detailed error server-side." Resolved: handler returns `"email delivery failed"` and logs the detailed error.

**Failure message:** Raw internal `err.Error()` returned to the NATS caller — leaks internals and breaks the documented stable error contract.

**Fix:** log the detailed error with `slog.ErrorContext`, then reply with the stable
documented error string. If you add a new caller-facing error, add it to the error table in
`docs/email-service-contract.md` in the same PR.

---

## `nats-handler-contract/mutual-exclusivity-not-enforced` — Important

**Pattern:** a request type documents "exactly one of field A or field B", but the handler
only validates that at least one is set and silently prefers one when both are present. This
hides caller bugs and contradicts the contract.

**Detect:** for `GetEmailStatusRequest` (or any request doc'd as "exactly one of …"), confirm
the handler rejects the both-set case with the documented error
(`"only one of email_id or group_id may be set"`), not just the neither-set case.

**Empirical citation:** PR #4 `internal/service/get_email_status_handler.go:40` — Copilot — "documented as requiring exactly one of `ses_message_id` or `correlation_id`, but the handler only validates that at least one is set. When both are provided, the handler silently prefers ... which contradicts the contract and can hide caller bugs." Maintainer agreed (handler redesigned; the exactly-one rule is now in `docs/email-service-contract.md`).

**Failure message:** "exactly one of" request fields not enforced — both-set requests silently pick one instead of erroring per the contract.

**Fix:** validate both the neither-set and both-set cases, returning the documented error
string for each, before doing any lookup.

---

## `nats-handler-contract/pkg-api-breaking-field-removal` — Important

**Pattern:** an exported field is removed from or renamed on a `pkg/api` wire type. `pkg/api`
is the public contract that external services import; removing exported fields is a
compile-time breaking change for callers using keyed struct literals, and dropping a JSON tag
silently changes the wire shape.

**Detect:** in a `pkg/api/nats.go` diff, find removed/renamed exported struct fields or changed
`json:` tags on `SendEmailRequest`, `SendEmailResponse`, `EmailRecipientRecord`,
`GetEmailStatusRequest`, the analytics types, etc.

**Empirical citation:** PR #6 `pkg/api/nats.go:44` — Copilot — "`SendEmailRequest` is part of the documented public `pkg/api` contract, so removing exported fields is a breaking compile-time change for external callers that use keyed struct literals. If compatibility matters ... keep these fields with deprecated comments". Also PR #8 `pkg/api/nats.go:75` flagged a field whose JSON tag lacked `omitempty`, changing the emitted wire shape.

**Failure message:** Breaking change to a public `pkg/api` wire type — removed/renamed exported field or changed JSON tag breaks external callers.

**Fix:** treat `pkg/api` changes as a coordinated breaking change: keep removed fields with a
deprecation comment where compatibility matters, preserve JSON tags / `omitempty`, and update
`docs/email-service-contract.md` in the same PR.

---

## `nats-handler-contract/pkg-api-doc-comment-drift` — Nit

**Pattern:** a `pkg/api` doc comment references a constant or field name that no longer exists
(e.g., references `SendSubject` when the exported constant is `SendEmailSubject`), misleading
callers who read the public package docs.

**Detect:** in a `pkg/api/nats.go` diff, cross-check each doc comment's referenced identifier
against the actual exported names in the file.

**Empirical citation:** PR #1 `pkg/api/nats.go:19` — Copilot — "`SendEmailRequest` doc comment refers to `SendSubject`, but the exported constant is `SendEmailSubject`. This makes the public contract documentation misleading for callers". Resolved by code change.

**Failure message:** `pkg/api` doc comment references a name that does not match the actual exported identifier.

**Fix:** update the doc comment to the real exported name.
