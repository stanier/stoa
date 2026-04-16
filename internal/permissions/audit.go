package permissions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditLogger logs permission decisions for compliance and debugging
type AuditLogger struct {
	file    *os.File
	mu      sync.Mutex
	enabled bool
}

// NewAuditLogger creates a new audit logger
func NewAuditLogger(path string, enabled bool) (*AuditLogger, error) {
	if !enabled {
		return &AuditLogger{enabled: false}, nil
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create audit log directory: %w", err)
	}

	// Open file for appending
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open audit log: %w", err)
	}

	return &AuditLogger{
		file:    f,
		enabled: true,
	}, nil
}

// Log writes an audit entry
func (l *AuditLogger) Log(entry AuditEntry) error {
	if !l.enabled {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Set timestamp if not set
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	// Sanitize sensitive parameters
	entry.Parameters = sanitizeParameters(entry.Parameters)

	// Marshal to JSON
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal audit entry: %w", err)
	}

	// Write JSONL format (one JSON object per line)
	data = append(data, '\n')
	_, err = l.file.Write(data)
	return err
}

// Close closes the audit log file
func (l *AuditLogger) Close() error {
	if !l.enabled || l.file == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	return l.file.Close()
}

// sanitizeParameters removes or redacts sensitive parameter values
func sanitizeParameters(params map[string]interface{}) map[string]interface{} {
	if params == nil {
		return nil
	}

	sanitized := make(map[string]interface{})

	// List of sensitive parameter names to redact
	sensitiveKeys := map[string]bool{
		"password":    true,
		"api_key":     true,
		"apikey":      true,
		"secret":      true,
		"token":       true,
		"credential":  true,
		"private_key": true,
		"privatekey":  true,
	}

	for key, value := range params {
		// Check if key contains sensitive words
		isSensitive := false
		for sensitiveKey := range sensitiveKeys {
			if contains(key, sensitiveKey) {
				isSensitive = true
				break
			}
		}

		if isSensitive {
			sanitized[key] = "[REDACTED]"
		} else {
			// For non-sensitive values, still truncate very long strings
			if str, ok := value.(string); ok && len(str) > 100 {
				sanitized[key] = str[:100] + "... [truncated]"
			} else {
				sanitized[key] = value
			}
		}
	}

	return sanitized
}

// contains checks if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	// Simple case-insensitive check
	// Convert to lowercase for comparison
	sLower := toLower(s)
	substrLower := toLower(substr)
	return containsSubstring(sLower, substrLower)
}

func toLower(s string) string {
	result := make([]rune, len(s))
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			result[i] = r + ('a' - 'A')
		} else {
			result[i] = r
		}
	}
	return string(result)
}

func containsSubstring(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
