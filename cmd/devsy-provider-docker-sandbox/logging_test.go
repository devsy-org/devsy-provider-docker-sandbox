package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

const (
	commandSecret = "COMMAND=secret-command"
	testProvider  = "provider"
	testDetail    = "detail"
	testWarn      = "warn"
)

func TestDiagnosticLevelsAndWireFormat(t *testing.T) {
	for _, tc := range []struct {
		level string
		want  int
	}{{"", 3}, {"trace", 5}, {"debug", 4}, {infoLevel, 3}, {testWarn, 2}, {"error", 1}} {
		t.Run(tc.level, func(t *testing.T) {
			var out bytes.Buffer
			logger, err := diagnosticLogger(
				&out,
				tc.level,
				"json",
				[]string{"API_TOKEN=canary-secret"},
			)
			if err != nil {
				t.Fatal(err)
			}
			for _, level := range []slog.Level{traceLevel, slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
				logger.Log(
					context.Background(),
					level,
					"message",
					"count",
					7,
					testDetail,
					"safe canary-secret",
				)
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			if len(lines) != tc.want {
				t.Fatalf("got %d records, want %d", len(lines), tc.want)
			}
			for _, line := range lines {
				assertDiagnosticRecord(t, line)
			}
		})
	}
}

func TestDiagnosticConfiguration(t *testing.T) {
	for _, tc := range []struct {
		level, format string
		valid         bool
	}{
		{"invalid-canary", jsonFormat, false},
		{infoLevel, "invalid-canary", false},
		{infoLevel, "text", true},
		{infoLevel, "plain", true},
		{infoLevel, "", true},
	} {
		var out bytes.Buffer
		logger, err := diagnosticLogger(&out, tc.level, tc.format, nil)
		if (err == nil) != tc.valid {
			t.Fatalf("configuration %q %q: %v", tc.level, tc.format, err)
		}
		if err != nil && strings.Contains(err.Error(), "invalid-canary") {
			t.Fatal("invalid value leaked")
		}
		if logger != nil {
			logger.Info("visible")
			if !strings.Contains(out.String(), "visible") {
				t.Fatal("missing output")
			}
		}
	}
}

func TestCanonicalEnvironmentFormatsAndLevels(t *testing.T) {
	for _, format := range []string{"", "auto", "AuTo", "JSON"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			logger, err := diagnosticLogger(&out, "WaRn", format, nil)
			if err != nil {
				t.Fatal(err)
			}
			logger.Info("filtered")
			logger.Warn("visible")
			var record map[string]any
			if err := json.Unmarshal(out.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			assertFields(t, record, map[string]any{"level": testWarn, slog.MessageKey: "visible"})
		})
	}
	for _, tc := range []struct{ level, format string }{
		{" INFO", "json"}, {"info", " auto"},
	} {
		_, err := diagnosticLogger(io.Discard, tc.level, tc.format, nil)
		if err == nil {
			t.Fatalf("accepted whitespace in %q %q", tc.level, tc.format)
		}
	}
}

func TestWarningAndExitDiagnostics(t *testing.T) {
	var out bytes.Buffer
	logger, err := diagnosticLogger(&out, "trace", "json", []string{commandSecret})
	if err != nil {
		t.Fatal(err)
	}
	w := compat.NewWarner(warningWriter{logger: logger.With("operation", "start")})
	w.Warn("safe secret-command")
	w.Warn("safe secret-command")
	if strings.Count(out.String(), "\n") != 1 || strings.Contains(out.String(), "secret-command") ||
		!strings.Contains(out.String(), "safe [REDACTED]") {
		t.Fatalf("warning: %s", out.String())
	}
}

func TestExitDiagnostics(t *testing.T) {
	var out bytes.Buffer
	logger, err := diagnosticLogger(&out, "trace", jsonFormat, []string{commandSecret})
	if err != nil {
		t.Fatal(err)
	}
	if got := exitCode(context.Canceled, logger); got != 130 || out.Len() != 0 {
		t.Fatalf("cancel: %d %s", got, out.String())
	}
	if got := exitCode(
		context.DeadlineExceeded,
		logger,
	); got != 124 ||
		!strings.Contains(out.String(), "deadline exceeded") {
		t.Fatalf("deadline: %d %s", got, out.String())
	}
}

func TestErrorDiagnosticRedaction(t *testing.T) {
	var out bytes.Buffer
	logger, err := diagnosticLogger(&out, "trace", jsonFormat, []string{commandSecret})
	if err != nil {
		t.Fatal(err)
	}
	if got := exitCode(
		errors.New("safe secret-command"),
		logger,
	); got != 1 || strings.Contains(out.String(), "secret-command") ||
		!strings.Contains(out.String(), "safe [REDACTED]") {
		t.Fatalf("failure: %d %s", got, out.String())
	}
}

func TestOverlappingSecretsAreFullyRedacted(t *testing.T) {
	var out bytes.Buffer
	logger, err := diagnosticLogger(&out, "info", jsonFormat, []string{
		"API_TOKEN=abc",
		"DB_PASSWORD=abc-secret-part",
		"OTHER_SECRET=part-tail",
	})
	if err != nil {
		t.Fatal(err)
	}
	logger.Error("failure", testDetail, "safe abc-secret-part-tail visible")
	var record map[string]any
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	assertFields(t, record, map[string]any{testDetail: "safe [REDACTED] visible"})
}

func TestMandatoryCompatibilityWarningBypassesErrorThreshold(t *testing.T) {
	var out bytes.Buffer
	logger, mandatory, err := diagnosticLoggers(
		&out,
		"error",
		"json",
		[]string{"API_TOKEN=secret-canary"},
	)
	if err != nil {
		t.Fatal(err)
	}
	logger.Warn("ordinary warning")
	warner := compat.NewWarner(warningWriter{logger: mandatory.With("operation", "status")})
	warner.Warn("compatibility detail secret-canary")
	warner.Warn("compatibility detail secret-canary")
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("expected one warning: %q", out.String())
	}
	var record map[string]any
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	assertFields(t, record, map[string]any{
		"level": testWarn, slog.MessageKey: "provider warning", "component": testProvider,
		testProvider: "docker-sandbox", "operation": "status",
		testDetail: "compatibility detail [REDACTED]",
	})
	if _, err := time.Parse("2006-01-02T15:04:05.000Z0700", record["ts"].(string)); err != nil {
		t.Fatal(err)
	}
}

func assertFields(t *testing.T, record, expected map[string]any) {
	t.Helper()
	for key, value := range expected {
		if record[key] != value {
			t.Fatalf("field %s: got %v, want %v", key, record[key], value)
		}
	}
}

func assertDiagnosticRecord(t *testing.T, line string) {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000Z0700", record["ts"].(string)); err != nil {
		t.Fatal(err)
	}
	assertFields(t, record, map[string]any{
		slog.MessageKey: "message", "count": float64(7), "component": testProvider,
		testProvider: "docker-sandbox", testDetail: "safe [REDACTED]",
	})
	if strings.Contains(line, "canary-secret") {
		t.Fatal("secret leaked")
	}
}
