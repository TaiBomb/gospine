package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

func TestSetLevel(t *testing.T) {
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"Warn", slog.LevelWarn},
		{"ERROR", slog.LevelError},
		{"verbose", slog.LevelInfo},
		{"", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			SetLevel(tt.in)

			if got := GlobalLevel.Level(); got != tt.want {
				t.Fatalf("expected level %v, got %v", tt.want, got)
			}
		})
	}
}

func TestFromContext_CarriesTheIDs(t *testing.T) {
	var buf bytes.Buffer

	previous := Log
	Log = &CustomLogger{slog.New(slog.NewJSONHandler(&buf, nil))}
	t.Cleanup(func() { Log = previous })

	ctx := ContextWithRequestID(context.Background(), "req-1")
	ctx = ContextWithCorrelationID(ctx, "corr-1")

	FromContext(ctx).Info("hello")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to decode the log line: %v", err)
	}

	if entry[RequestIDField] != "req-1" {
		t.Errorf("expected %s to be req-1, got %v", RequestIDField, entry[RequestIDField])
	}
	if entry[CorrelationIDField] != "corr-1" {
		t.Errorf("expected %s to be corr-1, got %v", CorrelationIDField, entry[CorrelationIDField])
	}
}

func TestFromContext_WithoutIDsReturnsTheBaseLogger(t *testing.T) {
	previous := Log
	Log = &CustomLogger{slog.Default()}
	t.Cleanup(func() { Log = previous })

	if got := FromContext(context.Background()); got != Log {
		t.Fatal("expected the base logger to be returned as is")
	}
}

func TestFromContext_WorksBeforeInitLogger(t *testing.T) {
	previous := Log
	Log = nil
	t.Cleanup(func() { Log = previous })

	if FromContext(context.Background()) == nil {
		t.Fatal("expected a logger backed by slog.Default")
	}
}

func TestMillis(t *testing.T) {
	if got := Millis(1234567 * time.Nanosecond); got != 1.235 {
		t.Fatalf("expected 1.235, got %v", got)
	}
}
