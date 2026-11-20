package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/devsy-org/devsy/logging"
	"github.com/devsy-org/devsy/logging/redact"
)

func diagnosticLogger(out io.Writer, level, format string, environ []string) (*slog.Logger, error) {
	config, invalid := logging.ResolveProvider(level, format)
	if invalid.Level {
		return nil, errors.New(
			"invalid DEVSY_LOG_LEVEL; expected trace, debug, info, warn, or error",
		)
	}
	if invalid.Format {
		return nil, errors.New(
			"invalid DEVSY_LOG_FORMAT; expected auto, json, text, plain, or logfmt",
		)
	}
	handler := logging.NewHandler(out, logging.HandlerOptions{
		Level:  config.Level,
		Format: config.Format,
		Redactor: redact.New(redact.Options{
			Environment:               environ,
			AdditionalEnvironmentKeys: []string{"COMMAND"},
			Mask:                      "[REDACTED]",
		}),
		TrustedAttrs: []slog.Attr{
			slog.String("component", "provider"),
			slog.String("provider", "docker-sandbox"),
		},
	})
	return slog.New(handler), nil
}

type warningWriter struct{ logger *slog.Logger }

func (w warningWriter) Write(p []byte) (int, error) {
	message := strings.TrimSuffix(strings.TrimPrefix(string(p), "warning: "), "\n")
	w.logger.Warn("provider warning", "detail", message)
	return len(p), nil
}

func logFailure(logger *slog.Logger, err error) {
	logger.Log(
		context.Background(),
		slog.LevelError,
		"provider operation failed",
		"detail",
		err.Error(),
	)
}

func diagnosticLoggers(
	out io.Writer,
	level, format string,
	environ []string,
) (ordinary, mandatory *slog.Logger, err error) {
	logger, err := diagnosticLogger(out, level, format, environ)
	if err != nil {
		return nil, nil, err
	}
	mandatory, err = diagnosticLogger(out, "warn", format, environ)
	if err != nil {
		return nil, nil, err
	}
	return logger, mandatory, nil
}
