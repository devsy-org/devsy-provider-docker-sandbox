package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
)

const (
	traceLevel = slog.Level(-8)
	jsonFormat = "json"
	infoLevel  = "info"
)

func diagnosticLevel(level string) (slog.Level, error) {
	levels := map[string]slog.Level{
		"trace": traceLevel,
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	level = strings.ToLower(level)
	if level == "" {
		level = infoLevel
	}
	selected, ok := levels[level]
	if !ok {
		return 0, errors.New("invalid DEVSY_LOG_LEVEL; expected trace, debug, info, warn, or error")
	}
	return selected, nil
}

func diagnosticLogger(out io.Writer, level, format string, environ []string) (*slog.Logger, error) {
	selected, err := diagnosticLevel(level)
	if err != nil {
		return nil, err
	}
	format = strings.ToLower(format)
	if format == "" || format == "auto" {
		format = jsonFormat
	}
	if format != jsonFormat && format != "text" && format != "plain" {
		return nil, errors.New("invalid DEVSY_LOG_FORMAT; expected auto, json, text, or plain")
	}
	options := &slog.HandlerOptions{Level: selected, ReplaceAttr: diagnosticAttrs(environ)}
	var handler slog.Handler
	if format == jsonFormat {
		handler = slog.NewJSONHandler(out, options)
	} else {
		handler = slog.NewTextHandler(out, options)
	}
	return slog.New(handler).With("component", "provider", "provider", "docker-sandbox"), nil
}

func sensitiveName(name string) bool {
	for _, part := range []string{"SECRET", "TOKEN", "PASSWORD", "CREDENTIAL", "API_KEY"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return name == "COMMAND"
}

func sensitiveValues(environ []string) []string {
	var secrets []string
	for _, entry := range environ {
		name, value, found := strings.Cut(entry, "=")
		if found && value != "" && sensitiveName(strings.ToUpper(name)) {
			secrets = append(secrets, value)
		}
	}
	return secrets
}

func diagnosticAttrs(environ []string) func([]string, slog.Attr) slog.Attr {
	secrets := sensitiveValues(environ)
	return func(_ []string, attr slog.Attr) slog.Attr {
		switch attr.Key {
		case slog.TimeKey:
			attr.Key = "ts"
			attr.Value = slog.StringValue(attr.Value.Time().Format("2006-01-02T15:04:05.000Z0700"))
		case slog.LevelKey:
			attr.Value = slog.StringValue(diagnosticLevelName(attr.Value))
		case "detail":
			if attr.Value.Kind() == slog.KindString {
				value := attr.Value.String()
				for _, secret := range secrets {
					value = strings.ReplaceAll(value, secret, "[REDACTED]")
				}
				attr.Value = slog.StringValue(value)
			}
		}
		return attr
	}
}

func diagnosticLevelName(value slog.Value) string {
	level, ok := value.Any().(slog.Level)
	if !ok {
		return strings.ToLower(value.String())
	}
	switch level {
	case traceLevel:
		return "trace"
	case slog.Level(12):
		return "fatal"
	default:
		return strings.ToLower(level.String())
	}
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
