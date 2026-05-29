<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Docs and Helm chart consistency

Patterns where service-owned docs (README, CLAUDE.md, the contract docs) or the service-local
Helm chart drift from the actual code, and chart-template issues that break deployment. The
contract docs and chart ship in the same PR as the behavior they describe, so doc-vs-code drift
is a repo-specific (not generic) review surface here.

**Read when:** `README.md`, `CLAUDE.md`, any file under `docs/**`, or any file under
`charts/lfx-v2-email-service/**` changed — *and* the diff also (or recently) changed an error
string, env var default, subject, KV bucket, or handler behavior the doc/chart describes.

---

## `docs-and-chart/error-table-drift` — Important

**Pattern:** a documented error-value table (README or `docs/email-service-contract.md`) lists
error strings that do not match what the handler actually returns — e.g. it shows `"invalid
request payload"` for a missing-field or KV failure that actually returns `"<field> is
required"` or `"internal error"`.

**Detect:** for any handler change that adds/changes a caller-facing error string, diff the
returned strings against the error tables in `README.md` and
`docs/email-service-contract.md`. They must enumerate the exact strings the handler returns.

**Empirical citation:** PR #6 `README.md:101` — Copilot — "This table does not match the handler's actual error responses: missing `email_id` returns `\"email_id is required\"`, and KV/unmarshal failures return `\"internal error\"`, not `\"invalid request payload\"`. Please list the distinct error strings returned by `GetEmailStatusHandler`". Same flagged at `README.md:139` for the analytics handler. Resolved by correcting the tables.

**Failure message:** Documented error table does not match the handler's actual returned error strings.

**Fix:** update the README / contract error table to the exact strings the handler returns; keep
`docs/email-service-contract.md` authoritative and in sync in the same PR.

---

## `docs-and-chart/env-default-doc-drift` — Important

**Pattern:** a documented env-var default contradicts the actual parse behavior or the chart —
e.g. docs say `EMAIL_ENABLED` defaults to `true`, but `parseEnv` selects `NoOpSender` unless
the value is explicitly `true`/`t`/`1`; or a secret-name default differs between
`values.yaml`, the PR description, and `CLAUDE.md`.

**Detect:** for any change to an env var or its default, cross-check the documented default in
`README.md` / `CLAUDE.md` against `cmd/email-service/config.go` parse behavior and the chart's
`values.yaml`.

**Empirical citation:** PR #1 `cmd/email-service/config.go:46` — Copilot — "The documented default for `EMAIL_ENABLED` is `true` ... but `parseEnv` defaults to `false` when the variable is unset ... running the binary locally without setting `EMAIL_ENABLED` silently selects the NoOpSender even though every doc says the opposite." Also PR #1 `charts/.../values.yaml:40` — "`smtpSecretName` defaults to `\"lfx-v2-email-service\"` here, but ... `CLAUDE.md` ... document the default as `lfx-v2-email-service-smtp`." Both resolved by reconciling docs to the real behavior.

**Failure message:** Documented env-var default contradicts the actual parse behavior or chart value.

**Fix:** make the docs match the real `parseEnv` behavior and the chart `values.yaml`; reconcile
all three (code, chart, docs) in the same PR.

---

## `docs-and-chart/namespace-template-inconsistency` — Important

**Pattern:** chart templates reference a values key that does not exist (e.g.
`.Values.lfx.namespace` with no `lfx.namespace` in `values.yaml`), so resources render with an
empty/defaulted `metadata.namespace`, inconsistent with templates that correctly use
`.Release.Namespace`.

**Detect:** in `charts/lfx-v2-email-service/templates/**`, confirm every `.Values.<path>`
referenced exists in `values.yaml`; for namespace, all templates should consistently use
`.Release.Namespace` (matching `deployment.yaml` / `service.yaml`).

**Empirical citation:** PR #1 `charts/lfx-v2-email-service/templates/serviceaccount.yaml:9` — Copilot — "`serviceaccount.yaml`, `secretstore.yaml`, and `externalsecret.yaml` reference `.Values.lfx.namespace`, but `values.yaml` does not define a `lfx.namespace` key. As a result, these resources will render with an empty `metadata.namespace` ... Either add `lfx.namespace` to `values.yaml` or change these templates to use `.Release.Namespace`". Resolved: all three switched to `.Release.Namespace`.

**Failure message:** Chart template references an undefined values key (or inconsistent namespace source) — renders an empty/wrong namespace.

**Fix:** reference only keys defined in `values.yaml`; use `.Release.Namespace` consistently for
namespace across all templates.

---

## `docs-and-chart/jetstream-cr-not-gated` — Important

**Pattern:** the chart unconditionally renders JetStream `KeyValue` CRs. In a cluster without
the JetStream CRDs/operator, `helm install/upgrade` fails — even though KV tracking is meant to
be optional at runtime.

**Detect:** in `charts/lfx-v2-email-service/templates/nats-kv-buckets.yaml`, confirm the KV CRs
are gated behind a values flag (e.g. `natsKVBuckets.enabled`) and/or a
`.Capabilities.APIVersions.Has` check.

**Empirical citation:** PR #4 `charts/lfx-v2-email-service/templates/nats-kv-buckets.yaml:27` — Copilot — "This template always renders JetStream `KeyValue` CRs. If the JetStream CRDs/operator are not installed in a target cluster, `helm install/upgrade` will fail even though KV tracking is meant to be optional at runtime. Consider gating these resources behind a value flag". Resolved: gated behind `natsKVBuckets.enabled` (default true).

**Failure message:** JetStream KV CRs rendered unconditionally — chart install fails on clusters without the JetStream operator.

**Fix:** gate the KV-bucket resources behind a values flag (default true) and/or a JetStream
capability check.
