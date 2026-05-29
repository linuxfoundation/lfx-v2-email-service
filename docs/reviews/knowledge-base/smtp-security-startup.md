<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# SMTP/MIME security and startup lifecycle

Patterns for the SMTP sender and MIME builder (header injection, address formatting, RNG
failure, PII redaction) and for the process lifecycle in `cmd/email-service/main.go` (graceful
shutdown, NATS drain, message context, health readiness). Security and crash patterns here are
promoted on cost-of-miss at a single occurrence.

**Read when:** any file under `internal/infrastructure/smtp/**`, `pkg/redaction/**`, or
`cmd/email-service/**` changed, or the diff touches MIME header construction, SMTP send,
shutdown/drain wiring, the NATS message context, or a health probe.

---

## `smtp-security-startup/header-injection` — Critical

**Pattern:** untrusted `to`, `subject`, or `from` values are interpolated directly into MIME
header lines. A CR/LF in any of them enables SMTP header injection (e.g. injecting `Bcc:`) and
can break MIME structure. ASCII-only subjects passed to `mime.QEncoding.Encode` are *not*
sanitized by the encoder.

**Detect:** in `internal/infrastructure/smtp/message.go`, confirm every value interpolated into
a header line is passed through `sanitizeHeaderValue` (strips `\r` / `\n`) — including the
`Subject:` value *before* `mime.QEncoding.Encode`, and the `X-SES-CONFIGURATION-SET` /
`X-LFX-TRACKING-ID` values.

**Empirical citation:** PR #1 `internal/infrastructure/smtp/message.go:41` — Copilot — "Email headers are built by interpolating untrusted `to` and `subject` directly into header lines. If either contains CR/LF, this enables SMTP header injection (e.g., adding Bcc/Cc) and can break the MIME structure." Plus PR #1 `message.go:48` — "Possible SMTP header injection via Subject ... For ASCII-only subjects, QEncoding may return the input unchanged ... Sanitize subject (remove \\r/\\n) before encoding". Resolved with `sanitizeHeaderValue` applied to every header value.

**Failure message:** Untrusted value interpolated into a MIME header without CR/LF sanitization — SMTP header-injection risk.

**Fix:** pass every interpolated header value through `sanitizeHeaderValue` (strip `\r` and
`\n`); apply it to the subject *before* `mime.QEncoding.Encode`, not after.

---

## `smtp-security-startup/recipient-pii-in-logs` — Critical

**Pattern:** a recipient email address (or other PII) is logged without redaction. The service
otherwise redacts addresses, so an unredacted recipient leaks PII — including from `NoOpSender`
when `EMAIL_ENABLED=false` in a non-local environment.

**Detect:** grep `internal/infrastructure/smtp/**` and any new log site for `req.To` / a raw
address in a log field without `redaction.RedactEmail(...)`. `go-conventions.md`: "Do not log
message bodies, SMTP credentials, SQS bodies, or full recipient addresses."

**Empirical citation:** PR #1 `internal/infrastructure/smtp/noop.go:22` — Copilot — "NoOpSender logs the raw recipient email address (and subject) even though the service otherwise redacts emails in logs. This can leak PII if EMAIL_ENABLED is disabled in non-local environments. Consider redacting the recipient (e.g., via pkg/redaction)". Resolved: `NoOpSender.Send` now uses `redaction.RedactEmail(req.To)`.

**Failure message:** Recipient address logged without `redaction.RedactEmail` — leaks PII into logs.

**Fix:** wrap every logged recipient address in `redaction.RedactEmail(addr)`; never log full
addresses, message bodies, SMTP credentials, or raw SQS bodies.

---

## `smtp-security-startup/ignored-crypto-rand-error` — Critical

**Pattern:** `crypto/rand.Read` errors are ignored when generating MIME boundaries or
Message-IDs. A silent RNG failure can yield predictable/zero values, risking boundary
collisions and non-unique Message-IDs (which the tracking design keys on).

**Detect:** in `message.go` `generateBoundary` / `generateMessageID`, confirm a `rand.Read`
error is handled (the repo chose to `panic` — an unrecoverable RNG failure should fail fast),
not discarded.

**Empirical citation:** PR #1 `internal/infrastructure/smtp/message.go:25` — Copilot — "crypto/rand.Read errors are ignored when generating MIME boundaries / Message-IDs. If the system RNG fails, this can silently produce predictable/zero values and risk boundary collisions or non-unique Message-IDs. Consider handling the error (and failing fast)". Resolved: both helpers `panic` on `rand.Read` error.

**Failure message:** `crypto/rand.Read` error ignored when generating a boundary/Message-ID — risks predictable, colliding identifiers.

**Fix:** handle the `rand.Read` error; the repo convention is to `panic("crypto/rand
unavailable: " + err.Error())` since the failure is unrecoverable and a fast crash beats
silent predictable output.

---

## `smtp-security-startup/from-header-double-wrap` — Important

**Pattern:** the `From:` header is built as `DisplayName <{from}>` while `cfg.From` may itself
be a full RFC 5322 address (`Name <addr>`), producing an invalid `From: Name <Name <addr>>`.

**Detect:** in `message.go`, confirm the `From:` value is built from the bare address extracted
via `mail.ParseAddress(cfg.From)`, not from the raw `cfg.From` string.

**Empirical citation:** PR #1 `internal/infrastructure/smtp/message.go:50` — Copilot — "The `From:` header is built as `LFX One <{from}>`, but `cfg.From` is parsed as a full RFC 5322 address ... If that happens, this header becomes invalid (`<Name <addr>>`). Consider parsing/formatting the address for the header". Resolved: `mail.ParseAddress(from)` extracts the bare address first.

**Failure message:** `From:` header built from a possibly-full RFC 5322 address — can emit an invalid `Name <Name <addr>>` header.

**Fix:** extract the bare address with `mail.ParseAddress(cfg.From)` and build the header from
that.

---

## `smtp-security-startup/exit-bypasses-graceful-shutdown` — Important

**Pattern:** a background goroutine (e.g. the SQS poller) calls `os.Exit(1)` directly on a
fatal runtime error, bypassing deferred cleanup — NATS drain, HTTP shutdown — and leaving logs
/ in-flight messages incomplete.

**Detect:** grep `os.Exit(` inside goroutines in `cmd/email-service/main.go`. A goroutine
runtime abort should signal shutdown (cancel context, set an atomic abort flag, signal `done`),
letting `main` drain and exit once.

**Empirical citation:** PR #4 `cmd/email-service/main.go:111` — Copilot — "Calling os.Exit(1) from inside this goroutine bypasses deferred cleanup (including NATS drain and HTTP shutdown) and can leave logs/telemetry incomplete. Prefer signaling shutdown (e.g., send SIGTERM on `done`, or call `cancel()` and let main exit after wg.Wait)". Resolved: poller now sets an `atomic.Bool`, cancels the context, signals `done`; `main` exits non-zero only after drain.

**Failure message:** Goroutine calls `os.Exit` directly — bypasses NATS drain and HTTP shutdown.

**Fix:** from a goroutine, signal shutdown (cancel context, set an atomic abort flag, send on
`done`) and let `main` run the normal drain/`wg.Wait` path, exiting non-zero at the end if the
abort flag is set.

---

## `smtp-security-startup/shutdown-waitgroup-leak` — Important

**Pattern:** a `WaitGroup` slot guarding the NATS connection is only `Done()`'d on one close
path (e.g. only when `ctx` was cancelled). On an unexpected close, the slot is never
decremented and `wg.Wait()` hangs forever, so the pod never terminates.

**Detect:** in `main.go`, confirm every lifecycle goroutine `defer wg.Done()` (or `Done()`s on
all close paths), the NATS drain has a bounded `natsgo.DrainTimeout`, and the message-handler
goroutine uses a context that survives the drain window.

**Empirical citation:** PR #1 `cmd/email-service/main.go:128` — Copilot — "NATS ClosedHandler only calls wg.Done() when ctx is canceled. If the connection closes unexpectedly (ctx.Err()==nil), main will still wait on wg forever ... Ensure wg.Done() is called on all close paths." Plus PR #4 `main.go:147` — "NATS drain no longer has a bounded timeout ... `wg.Wait()` can block indefinitely during shutdown and pods may hang on termination. Reintroduce an explicit drain timeout." Both resolved (unconditional `wg.Done()`, `natsgo.DrainTimeout`).

**Failure message:** WaitGroup slot not released on every close path (or unbounded NATS drain) — `wg.Wait()` can hang and the pod never terminates.

**Fix:** `defer wg.Done()` (or release on all close paths), bound the NATS drain with
`natsgo.DrainTimeout(...)`, and run the message-handler callback on a context that is only
cancelled after drain completes.

---

## `smtp-security-startup/readiness-ignores-nats` — Important

**Pattern:** `/readyz` always returns 200 even when the service cannot process messages (NATS
disconnected, reconnecting, or drained). Traffic gets routed to an unhealthy pod because the
core dependency (NATS) is not reflected in readiness.

**Detect:** in `main.go`, confirm `/readyz` reflects NATS health (e.g. `nc.IsConnected()` →
503 when not connected). `/livez` stays unconditional.

**Empirical citation:** PR #1 `cmd/email-service/main.go:159` — Copilot — "`/readyz` always returns 200 even if the service is unable to process messages (e.g., NATS disconnected/reconnecting or drained). Since this service's core dependency is NATS, readiness should reflect NATS connection/subscription health". Resolved: `/readyz` returns 503 when `nc.IsConnected()` is false.

**Failure message:** `/readyz` returns 200 regardless of NATS connection state — unhealthy pods keep receiving traffic.

**Fix:** gate `/readyz` on `nc.IsConnected()` (return 503 when disconnected/reconnecting/drained);
keep `/livez` unconditional.
