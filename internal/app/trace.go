package app

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/trace"
)

// SetupTracer creates a conversation tracer with a filename of the form
// trace_{YYYY-MM-DD}_{sessionID}.jsonl. If sessionID is empty a random UUID is
// generated. Always returns a non-nil *trace.Tracer (no-op when tracing is disabled).
func SetupTracer(cfg *config.Config, sessionID string) *trace.Tracer {
	tracer := &trace.Tracer{}
	if cfg == nil || !cfg.Trace.Enabled {
		return tracer
	}

	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	date := time.Now().Format("2006-01-02")
	tracePath := filepath.Join(cfg.Trace.Dir, fmt.Sprintf("trace_%s_%s.jsonl", date, sessionID))

	t, err := trace.NewTracer(tracePath, true)
	if err != nil {
		logging.Warnf("Failed to initialize tracer: %v", err)
		return tracer
	}

	_ = t.LogEvent(trace.TraceEvent{
		Type:     "session_start",
		Metadata: map[string]interface{}{"session_id": sessionID},
	})

	logging.Infof("Conversation trace enabled: %s", tracePath)
	return t
}
