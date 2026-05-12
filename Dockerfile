# Copyright The Linux Foundation and each contributor to LFX.
# SPDX-License-Identifier: MIT

# checkov:skip=CKV_DOCKER_7:No free access to Chainguard versioned labels.
# hadolint global ignore=DL3007

FROM cgr.dev/chainguard/go:latest AS builder

ARG TARGETARCH
ENV CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -o /go/bin/email-service -trimpath -ldflags="-w -s" github.com/linuxfoundation/lfx-v2-email-service/cmd/email-service

FROM cgr.dev/chainguard/static:latest

EXPOSE 8080

USER nonroot

COPY --from=builder /go/bin/email-service /cmd/email-service

ENTRYPOINT ["/cmd/email-service"]
