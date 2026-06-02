// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Version, BuildTime, and GitCommit are injected at build time via -ldflags.
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

type environment struct {
	NatsURL             string
	Port                string
	EmailEnabled        bool
	SESEventingEnabled  bool
	SMTP                smtpConfig
	SESConfigurationSet string
	SESEngagementSQSURL string
}

type smtpConfig struct {
	Host                  string
	Port                  int
	From                  string
	FromDisplayName       string   // display name for the From header; default "LFX Self Serve"
	AllowedFromDomains    []string // lower-cased exact-match domains permitted for per-message From override
	AllowedReplyToDomains []string // lower-cased base domains permitted for reply_to (subdomain suffix matching)
	Username              string
	Password              string
}

func parseEnv() environment {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	emailEnabledVal := os.Getenv("EMAIL_ENABLED")
	emailEnabled := emailEnabledVal == "true" || emailEnabledVal == "t" || emailEnabledVal == "1"

	sesEventingEnabledVal := os.Getenv("SES_EVENTING_ENABLED")
	sesEventingEnabled := sesEventingEnabledVal == "true" || sesEventingEnabledVal == "t" || sesEventingEnabledVal == "1"

	smtpHost := os.Getenv("SMTP_HOST")
	if smtpHost == "" {
		smtpHost = "localhost"
	}

	smtpPort := 587
	if raw := os.Getenv("SMTP_PORT"); raw != "" {
		if p, err := strconv.Atoi(raw); err == nil {
			smtpPort = p
		} else {
			slog.Warn("invalid SMTP_PORT, using default 587", "value", raw)
		}
	}

	// DEFAULT_SMTP_FROM is the primary env var. Fall back to the legacy SMTP_FROM
	// so existing deployments that haven't migrated yet don't silently revert to
	// the hardcoded default and send mail from the wrong address.
	smtpFrom := os.Getenv("DEFAULT_SMTP_FROM")
	if smtpFrom == "" {
		smtpFrom = os.Getenv("SMTP_FROM")
	}
	if smtpFrom == "" {
		smtpFrom = "noreply@lfx.linuxfoundation.org"
	}

	smtpFromDisplayName := os.Getenv("DEFAULT_SMTP_FROM_DISPLAY_NAME")
	if smtpFromDisplayName == "" {
		smtpFromDisplayName = "LFX Self Serve"
	}

	// SMTP_ALLOWED_FROM_DOMAINS is a comma-separated list of domains permitted
	// for per-message From overrides (e.g. "lfx.linuxfoundation.org,linuxfoundation.org").
	// Defaults to "lfx.linuxfoundation.org" when the variable is unset.
	// Set it to an explicit empty string ("") to disable per-message From overrides entirely.
	// os.LookupEnv is used to distinguish "unset" (apply default) from "set to empty" (disable).
	var allowedFromDomains []string
	allowedDomainsRaw, allowedDomainsSet := os.LookupEnv("SMTP_ALLOWED_FROM_DOMAINS")
	if !allowedDomainsSet {
		allowedDomainsRaw = "lfx.linuxfoundation.org"
	}
	for _, d := range strings.Split(allowedDomainsRaw, ",") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			allowedFromDomains = append(allowedFromDomains, d)
		}
	}

	// SMTP_ALLOWED_REPLY_TO_DOMAINS is a comma-separated list of base domains permitted
	// for per-message reply_to overrides. Subdomain suffix matching applies, so
	// "linuxfoundation.org" also permits "lfx.linuxfoundation.org".
	// Defaults to "linuxfoundation.org" when unset. Set to "" to disable reply_to overrides.
	var allowedReplyToDomains []string
	allowedReplyToRaw, allowedReplyToSet := os.LookupEnv("SMTP_ALLOWED_REPLY_TO_DOMAINS")
	if !allowedReplyToSet {
		allowedReplyToRaw = "linuxfoundation.org"
	}
	for _, d := range strings.Split(allowedReplyToRaw, ",") {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			allowedReplyToDomains = append(allowedReplyToDomains, d)
		}
	}

	return environment{
		NatsURL:             natsURL,
		Port:                port,
		EmailEnabled:        emailEnabled,
		SESEventingEnabled:  sesEventingEnabled,
		SESConfigurationSet: os.Getenv("SES_CONFIGURATION_SET"),
		SESEngagementSQSURL: os.Getenv("SES_ENGAGEMENT_SQS_QUEUE_URL"),
		SMTP: smtpConfig{
			Host:                  smtpHost,
			Port:                  smtpPort,
			From:                  smtpFrom,
			FromDisplayName:       smtpFromDisplayName,
			AllowedFromDomains:    allowedFromDomains,
			AllowedReplyToDomains: allowedReplyToDomains,
			Username:              os.Getenv("SMTP_USERNAME"),
			Password:              os.Getenv("SMTP_PASSWORD"),
		},
	}
}
