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
	"github.com/devsy-org/devsy/logging"
)

const (
	testWholeMask    = "[REDACTED]"
	testProviderName = "docker-sandbox"
	commandSecret    = "COMMAND=secret-command"
	testProvider     = "provider"
	testDetail       = "detail"
	testWarn         = "warn"
	jsonFormat       = "json"
	infoLevel        = "info"
	invalidCanary    = "invalid-canary"
	testComponent    = "component"
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
			for _, level := range []slog.Level{
				logging.LevelTrace, slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError,
			} {
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
		{invalidCanary, jsonFormat, false},
		{infoLevel, invalidCanary, false},
		{infoLevel, "text", true},
		{infoLevel, "plain", true},
		{infoLevel, "", true},
		{"INFO", "PlAiN", true},
		{infoLevel, "logfmt", true},
		{infoLevel, "LOGFMT", true},
		{"info ", "json", false},
		{infoLevel, "json ", false},
	} {
		var out bytes.Buffer
		logger, err := diagnosticLogger(&out, tc.level, tc.format, nil)
		if (err == nil) != tc.valid {
			t.Fatalf("configuration %q %q: %v", tc.level, tc.format, err)
		}
		if err != nil && strings.Contains(err.Error(), invalidCanary) {
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

func TestDiagnosticConfigurationErrorWording(t *testing.T) {
	for _, tc := range []struct{ level, format, want string }{
		{invalidCanary, invalidCanary, "invalid DEVSY_LOG_LEVEL; expected trace, debug, info, warn, or error"},
		{infoLevel, invalidCanary, "invalid DEVSY_LOG_FORMAT; expected auto, json, text, plain, or logfmt"},
	} {
		_, err := diagnosticLogger(io.Discard, tc.level, tc.format, nil)
		if err == nil || err.Error() != tc.want {
			t.Fatalf("got error %v, want %q", err, tc.want)
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
	for _, format := range []string{"logfmt", "LoGfMt"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			logger, err := diagnosticLogger(&out, "WaRn", format, nil)
			if err != nil {
				t.Fatal(err)
			}
			logger.Info("filtered")
			logger.Warn("visible")
			if !strings.Contains(out.String(), "msg=visible") ||
				strings.Contains(out.String(), "filtered") {
				t.Fatalf("unexpected logfmt output: %q", out.String())
			}
		})
	}
}

func TestCanonicalEnvironmentRejectsWhitespace(t *testing.T) {
	for _, tc := range []struct{ level, format string }{
		{" INFO", "json"}, {infoLevel, " auto"}, {infoLevel, "logfmt "},
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
		!strings.Contains(out.String(), `"detail":"[REDACTED]"`) {
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
		!strings.Contains(out.String(), `"detail":"[REDACTED]"`) {
		t.Fatalf("failure: %d %s", got, out.String())
	}
}

func TestDiagnosticRedactsMessageAndProtectsIdentity(t *testing.T) {
	var out bytes.Buffer
	logger, err := diagnosticLogger(&out, infoLevel, "json", []string{"command=secret-command"})
	if err != nil {
		t.Fatal(err)
	}
	logger.With(testComponent, "impostor").
		Error("safe secret-command", "provider", "impostor", "detail", "secret-command")
	var record map[string]any
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	assertFields(t, record, map[string]any{
		testComponent: testProvider, testProvider: testProviderName,
		slog.MessageKey: "safe [REDACTED]", testDetail: testWholeMask,
	})
	if strings.Contains(out.String(), "secret-command") ||
		strings.Contains(out.String(), "impostor") {
		t.Fatalf("unsafe output: %s", out.String())
	}
}

func TestOverlappingSecretsAreFullyRedacted(t *testing.T) {
	var out bytes.Buffer
	logger, err := diagnosticLogger(&out, infoLevel, jsonFormat, []string{
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
	assertFields(t, record, map[string]any{testDetail: testWholeMask})
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
		"level": testWarn, slog.MessageKey: "provider warning", testComponent: testProvider,
		testProvider: testProviderName, "operation": "status",
		testDetail: testWholeMask,
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
		slog.MessageKey: "message", "count": float64(7), testComponent: testProvider,
		testProvider: testProviderName, testDetail: testWholeMask,
	})
	if strings.Contains(line, "canary-secret") {
		t.Fatal("secret leaked")
	}
}
