<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Tracking KV and SES/SQS engagement

Patterns for the engagement-tracking surface: KV writes from `SendEmailHandler`, the
`email-recipients` / `email-group-index` buckets, the SES → SNS → SQS event pipeline, the SQS
poller, and `EngagementEventHandler`. This is a data-integrity-heavy area — concurrent KV
updates, lost SQS messages, out-of-order delivery, and tracking-ID parsing all carry a high
cost of miss, so most entries here are promoted on cost-of-miss even at a single occurrence.

**Read when:** any file under `internal/service/engagement_event_handler*.go`,
`internal/service/send_email_handler*.go` (KV write path), `internal/infrastructure/sqs/**`,
or a diff touching `email-recipients` / `email-group-index`, the `X-LFX-TRACKING-ID` header,
or SES event handling.

---

## `tracking-kv-engagement/blind-put-no-optimistic-lock` — Critical

**Pattern:** a KV record is updated with read-`Get`-then-`Put` (last-write-wins). When two
events for the same email (e.g. DELIVERY and OPEN), or two replicas polling the same SQS
queue, are processed concurrently, increments and timestamps are lost.

**Detect:** in `internal/service/engagement_event_handler.go` (or any concurrent KV writer),
find a `Get(...)` followed by a `Put(...)` on the same key with no `Update(key, value,
revision)` and no retry. Concurrent KV writers must use `Update` with `entry.Revision()` and
retry on conflict.

**Empirical citation:** PR #4 `internal/service/engagement_event_handler.go:142` — Copilot — "KV updates use a read-modify-write with `Get` followed by `Put`, which is last-write-wins. If multiple instances/processes handle events for the same message concurrently ... open-count increments and timestamps can be lost. Use optimistic concurrency (e.g., `Update` with the entry revision + retry on revision mismatch)." Flagged twice in PR #4 (lines 142 and 135). Resolved by switching to `Update` + revision + single retry.

**Failure message:** Blind `Get`+`Put` on a concurrently-written KV record — concurrent engagement events will lose updates. Use optimistic locking.

**Fix:** read the entry, apply the change, then `recipientsKV.Update(key, value,
entry.Revision())`; retry once on a revision-mismatch conflict (the established
`appendToGroupIndex` pattern).

---

## `tracking-kv-engagement/lost-sqs-message-on-kv-failure` — Critical

**Pattern:** `EngagementEventHandler.Handle` returns `nil` even when the KV update fails after
retries. The poller deletes the SQS message on a `nil` return, so the engagement event is
permanently lost (never reaches the DLQ).

**Detect:** in `engagement_event_handler.go`, confirm `Handle` returns a non-nil error when the
KV `Update` fails after its retry budget. A failed durable write must not return `nil`.

**Empirical citation:** PR #4 `internal/service/engagement_event_handler.go:135` — Copilot — "If updating the recipient record fails even after the retry, `Handle` still returns nil, so the poller will delete the SQS message and the engagement event is permanently lost. Return a non-nil error when the KV update fails (after retries) so the message remains in the queue / can go to the DLQ." Resolved by returning a non-nil error on persistent failure.

**Failure message:** Engagement handler returns `nil` after a failed KV write — the SQS message is deleted and the event is lost instead of reaching the DLQ.

**Fix:** return a non-nil error from `Handle` when the KV update fails after retries; the
poller leaves the message on the queue. Reserve the `nil`-return skip for genuinely
non-retryable cases (unknown event type, missing tracking header, malformed payload).

---

## `tracking-kv-engagement/group-id-tracking-id-split` — Critical

**Pattern:** the `X-LFX-TRACKING-ID` header is `<group_id>/<email_id>`, and `group_id` is
caller-supplied and may contain `/`. Splitting on the *first* `/` extracts the wrong
`email_id`, so the wrong KV record (or none) is updated.

**Detect:** in `engagement_event_handler.go`, the tracking-ID parse must use
`strings.LastIndex(v, "/")` (take the segment after the last `/`), not `strings.Index` /
`SplitN` on the first separator.

**Empirical citation:** PR #4 `internal/service/engagement_event_handler.go:207` — Copilot — "`extractEmailID` splits `X-LFX-TRACKING-ID` on the first '/'. Because `group_id` is caller-supplied ... a group_id containing '/' would cause incorrect email_id extraction and KV lookups. Consider splitting on the last '/'". Resolved by switching to `strings.LastIndex`. `docs/email-engagement-tracking.md` now documents "extracts the email ID from the part after the last `/`, so group IDs may contain `/`."

**Failure message:** Tracking-ID parsed on the first `/` — a `group_id` containing `/` extracts the wrong `email_id` and updates the wrong KV record.

**Fix:** parse with `strings.LastIndex(trackingID, "/")` and take the trailing segment as the
`email_id`.

---

## `tracking-kv-engagement/orphaned-group-index-entry` — Important

**Pattern:** the group index is appended even when the recipient-record `Put` fails, leaving
`email-group-index` referencing an `email_id` that has no `email-recipients` record. Status and
analytics queries then undercount or return "not found" for indexed IDs.

**Detect:** in `send_email_handler.go` `writeTrackingRecords`, confirm `appendToGroupIndex` is
only called *after* the recipient `Put` succeeds (the `Put` error path returns early).

**Empirical citation:** PR #4 `internal/service/send_email_handler.go:99` — Copilot — "`writeTrackingRecords` appends to the group index even when writing the recipient record fails. This can leave the group index containing email IDs that have no corresponding recipient record, causing status/analytics queries to undercount or return 'not found'". Resolved: index append now gated on a successful recipient `Put`.

**Failure message:** Group index appended even when the recipient-record write failed — produces orphaned index entries that break status/analytics queries.

**Fix:** only call `appendToGroupIndex` after the recipient `Put` succeeds; on a `Put` error,
log a warning and return early.

---

## `tracking-kv-engagement/group-index-overwrite-on-transient-error` — Important

**Pattern:** `appendToGroupIndex` treats any `Get` error (including transient) the same as
"not found", and ignores `json.Unmarshal` errors on the existing index value — so a transient
read error or a corrupted entry silently drops the whole existing list and replaces it with
just the new `email_id`.

**Detect:** in `appendToGroupIndex`, confirm the `Get` error path uses
`errors.Is(err, natsgo.ErrKeyNotFound)` to distinguish a missing key (expected on first write)
from a transient error, and that unmarshal errors are logged/handled rather than ignored.

**Empirical citation:** PR #4 `internal/service/send_email_handler.go:135` — Copilot — "appendToGroupIndex ignores JSON unmarshal errors for the existing index value. If the stored value is corrupted/unexpected, this will silently drop the previous list ... It also treats any Get error the same as 'not found', potentially overwriting the index on transient errors. Consider handling nats.ErrKeyNotFound separately, logging unmarshal errors, and bailing out on other Get/Update errors." Resolved by adding the `ErrKeyNotFound` distinction and early-return on transient errors.

**Failure message:** Group-index `Get`/unmarshal error treated as "empty" — a transient error or corrupt entry silently overwrites the whole index.

**Fix:** branch the `Get` error on `errors.Is(err, natsgo.ErrKeyNotFound)`; on a transient
error log and return early; log unmarshal errors instead of resetting the list silently.

---

## `tracking-kv-engagement/wrong-event-timestamp` — Important

**Pattern:** engagement timestamps (`delivered_at`, `opened_at`, `failed_at`) are recorded with
`time.Now()` instead of the SES event's own timestamp. With SQS delays/retries the stored time
can misrepresent when the engagement actually happened, and the parsed SES timestamp fields go
unused.

**Detect:** in `engagement_event_handler.go`, confirm event timestamps come from the parsed SES
event (RFC3339, via `parseTimestamp`) with `time.Now().UTC()` only as a fallback when the field
is absent or unparseable.

**Empirical citation:** PR #4 `internal/service/engagement_event_handler.go:177` — Copilot — "Tracking timestamps are currently recorded using `time.Now().UTC()` rather than the SES event's `timestamp` fields. This can misrepresent when the engagement actually happened (especially if messages are delayed in SQS) ... Parse and apply the SES-provided timestamps (with a safe fallback to `time.Now()` if parsing fails)." Resolved with a `parseTimestamp` helper used for all four event types.

**Failure message:** Engagement timestamp set from `time.Now()` instead of the SES event timestamp — SQS delays will record the wrong engagement time.

**Fix:** use the SES-provided RFC3339 timestamp via `parseTimestamp(...)`, falling back to
`time.Now().UTC()` only when the field is missing or unparseable.

---

## `tracking-kv-engagement/last-opened-at-out-of-order` — Important

**Pattern:** `last_opened_at` is assigned to the most-recently-processed event unconditionally.
SES/SNS/SQS does not guarantee in-order delivery, so a delayed or replayed older open can
overwrite a newer `LastOpenedAt`.

**Detect:** in `engagement_event_handler.go`, confirm `record.LastOpenedAt` is only advanced
when `record.LastOpenedAt == nil || t.After(*record.LastOpenedAt)` (while still appending the
event to the dedup list).

**Empirical citation:** PR #8 `internal/service/engagement_event_handler.go:165` — Copilot — "`last_opened_at` is assigned to the most recently processed event, not necessarily the latest open timestamp. The SES/SNS/SQS pipeline does not guarantee open events are delivered in timestamp order ... update this only when it is nil or `t.After(*record.LastOpenedAt)`". Resolved by the strictly-after guard.

**Failure message:** `last_opened_at` advanced unconditionally — out-of-order SQS delivery can overwrite a newer open timestamp with an older one.

**Fix:** only set `LastOpenedAt` when `record.LastOpenedAt == nil || t.After(*record.LastOpenedAt)`; still append every open event to the dedup list.

---

## `tracking-kv-engagement/poller-prereqs-not-validated` — Important

**Pattern:** the SQS poller is started when `SES_EVENTING_ENABLED=true` without first
validating that `SES_ENGAGEMENT_SQS_QUEUE_URL` is non-empty and the recipients KV is non-nil.
The poller then either hammers an invalid queue URL or panics on a nil `KeyValue`.

**Detect:** in `cmd/email-service/main.go`, confirm the SES-eventing startup path fails fast
(fatal log + exit) when the queue URL is empty or the recipients KV is nil, *before* starting
the poller goroutine. `docs/email-engagement-tracking.md` states these are fatal-at-startup.

**Empirical citation:** PR #4 `cmd/email-service/main.go:114` — Copilot — "When SES_EVENTING_ENABLED is true, the SQS poller is started even if SES_ENGAGEMENT_SQS_QUEUE_URL is empty or recipientsKV is nil ... That will either cause AWS ReceiveMessage to fail repeatedly on an invalid queue URL or panic in EngagementEventHandler on a nil KeyValue. Consider validating these prerequisites ... and fail fast." Resolved by fatal startup guards.

**Failure message:** SQS poller started without validating queue URL / recipients KV — risks a nil-KV panic or a tight failing-receive loop.

**Fix:** when `SES_EVENTING_ENABLED=true`, validate a non-empty queue URL and non-nil
recipients KV at startup; log fatal and exit if either is missing, before any poller goroutine
starts.

---

## `tracking-kv-engagement/poller-no-backoff-on-receive-error` — Important

**Pattern:** the SQS poller logs and immediately retries on `ReceiveMessage` errors with no
backoff. On a persistent network/IAM outage this hot-loops, spamming logs and burning CPU.

**Detect:** in `internal/infrastructure/sqs/poller.go`, confirm the `ReceiveMessage` error path
applies a capped backoff between consecutive failures and bails out after a bounded number of
consecutive errors (current code: linear `consecutiveErrors * time.Second` capped at 30s,
abort after `maxConsecutiveErrors`).

**Empirical citation:** PR #4 `internal/infrastructure/sqs/poller.go:85` — Copilot — "On `ReceiveMessage` errors, the poller logs and immediately retries with no backoff. In cases of persistent failures (network/IAM outages), this can hot-loop and spam logs/consume CPU. Add a small exponential backoff (capped)." Resolved by adding capped backoff + consecutive-error abort.

**Failure message:** SQS poller retries `ReceiveMessage` errors with no backoff — a persistent outage hot-loops and spams logs.

**Fix:** apply a capped backoff between consecutive receive failures (with early exit on
context cancellation), and abort the poller after a bounded number of consecutive errors so the
process can exit and restart.
