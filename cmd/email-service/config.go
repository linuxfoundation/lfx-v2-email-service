// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"log/slog"
	"os"
	"strconv"
)

// Version, BuildTime, and GitCommit are injected at build time via -ldflags.
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

type environment struct {
	NatsURL      string
	Port         string
	EmailEnabled bool
	SMTP         smtpConfig
}

type smtpConfig struct {
	Host     string
	Port     int
	From     string
	Username string
	Password string
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

	smtpFrom := os.Getenv("SMTP_FROM")
	if smtpFrom == "" {
		smtpFrom = "noreply@lfx.linuxfoundation.org"
	}

	return environment{
		NatsURL:      natsURL,
		Port:         port,
		EmailEnabled: emailEnabled,
		SMTP: smtpConfig{
			Host:     smtpHost,
			Port:     smtpPort,
			From:     smtpFrom,
			Username: os.Getenv("SMTP_USERNAME"),
			Password: os.Getenv("SMTP_PASSWORD"),
		},
	}
}
