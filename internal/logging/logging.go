// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package logging configures structured logging for the email service.
package logging

import (
	"context"
	"log"
	"log/slog"
	"os"
)

// ErrKey is the standard slog key for error values.
const ErrKey = "error"

type ctxKey string

const slogFields ctxKey = "slog_fields"

type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if attrs, ok := ctx.Value(slogFields).([]slog.Attr); ok {
		for _, v := range attrs {
			r.AddAttrs(v)
		}
	}
	return h.Handler.Handle(ctx, r)
}

// AppendCtx adds an slog attribute to ctx so it is included in all records
// created with that context.
func AppendCtx(parent context.Context, attr slog.Attr) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	if v, ok := parent.Value(slogFields).([]slog.Attr); ok {
		newV := make([]slog.Attr, len(v), len(v)+1)
		copy(newV, v)
		newV = append(newV, attr)
		return context.WithValue(parent, slogFields, newV)
	}
	return context.WithValue(parent, slogFields, []slog.Attr{attr})
}

// InitStructureLogConfig configures the global slog logger from environment variables.
func InitStructureLogConfig() {
	opts := &slog.HandlerOptions{}
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		opts.Level = slog.LevelDebug
	case "warn":
		opts.Level = slog.LevelWarn
	case "error":
		opts.Level = slog.LevelError
	default:
		opts.Level = slog.LevelInfo
	}
	addSource := os.Getenv("LOG_ADD_SOURCE")
	opts.AddSource = addSource == "true" || addSource == "1"

	log.SetFlags(log.Llongfile)
	slog.SetDefault(slog.New(contextHandler{slog.NewJSONHandler(os.Stdout, opts)}))
	slog.Info("log config", "level", opts.Level, "addSource", opts.AddSource)
}
