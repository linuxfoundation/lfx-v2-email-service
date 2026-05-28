<!-- Copyright The Linux Foundation and each contributor to LFX. -->
<!-- SPDX-License-Identifier: MIT -->

# Service Helm Chart

This document owns the local chart interface for `lfx-v2-email-service`.

Shared chart conventions live in `lfx-v2-helm/docs/service-chart-patterns.md`. Deployed values, image tags, IRSA role annotations, and environment promotion live in `lfx-v2-argocd`.

## Chart Location

`charts/lfx-v2-email-service/`

Important templates:

| Template | Purpose |
| --- | --- |
| `deployment.yaml` | Container image, env vars, probes, resources. |
| `service.yaml` | ClusterIP service for health probes. |
| `nats-kv-buckets.yaml` | JetStream KV buckets for tracking records and group indexes. |
| `externalsecret.yaml` | Optional ExternalSecret resources for SMTP and SES engagement config. |
| `secretstore.yaml` | SecretStore used by External Secrets Operator. |
| `serviceaccount.yaml` | ServiceAccount, including optional IRSA annotations. |

This service does not expose a public HTTP API through Gateway/Heimdall. Its business surface is NATS request/reply. The HTTP server only serves `/livez` and `/readyz`.

## Runtime Values

| Value | Env var | Notes |
| --- | --- | --- |
| `nats.url` | `NATS_URL` | NATS server URL. |
| `app.logLevel` | `LOG_LEVEL` | `debug`, `info`, `warn`, or `error`. |
| `app.logAddSource` | `LOG_ADD_SOURCE` | Adds source file/line to logs. |
| `app.email.enabled` | `EMAIL_ENABLED` | `false` uses `NoOpSender`. |
| `app.email.smtpHost` | `SMTP_HOST` | SMTP hostname. |
| `app.email.smtpPort` | `SMTP_PORT` | SMTP port. |
| `app.email.smtpFrom` | `SMTP_FROM` | Envelope sender address. |
| `app.email.smtpSecretName` | `SMTP_USERNAME`, `SMTP_PASSWORD` | Secret keys must be `smtp-username` and `smtp-password`. |
| `app.ses.eventingEnabled` | `SES_EVENTING_ENABLED` | Starts the SQS engagement poller. |
| `app.ses.engagementSecretName` | `SES_CONFIGURATION_SET`, `SES_ENGAGEMENT_SQS_QUEUE_URL` | Secret keys must be `ses_configuration_set_name` and `sqs_queue_url`. |
| `app.extraEnv` | varies | Escape hatch for additional env vars. |
| `app.otel.*` | `OTEL_*` | OpenTelemetry configuration. |

## NATS KV Buckets

`natsKVBuckets.enabled=true` renders two JetStream `KeyValue` custom resources:

| Bucket | Purpose |
| --- | --- |
| `email-recipients` | One tracking record per sent email. |
| `email-group-index` | Group ID to email ID list. |

If the operator CRDs are not installed or the buckets are absent, the service still sends email, but tracking, status lookup, and analytics are disabled.

## Secrets

When `externalSecretsOperator.enabled=true` and `global.awsRegion` is set:

- One ExternalSecret may create the SMTP credential secret named after the chart.
- A second ExternalSecret creates `app.ses.engagementSecretName` for SES engagement config.

Secret values and AWS source resources are not owned here. This chart owns only the Kubernetes references and target key names.

## Local Development

Use `charts/lfx-v2-email-service/values.local.example.yaml` as the starting point for local overrides. The local values file is gitignored at `charts/lfx-v2-email-service/values.local.yaml`.

Common local pattern:

- Run NATS locally or through the local platform stack.
- Use Mailpit for SMTP capture.
- Set `EMAIL_ENABLED=true` only when a real or local SMTP endpoint is reachable.
- Keep `SES_EVENTING_ENABLED=false` unless AWS credentials, queue URL, and NATS KV are configured.

## Change Checklist

- Update `CLAUDE.md` and this document when adding or renaming chart values.
- Update `docs/email-service-contract.md` or `docs/email-engagement-tracking.md` when chart changes affect public behavior.
- Coordinate deployed values with `lfx-v2-argocd`.
- Coordinate shared chart conventions with `lfx-v2-helm`.
