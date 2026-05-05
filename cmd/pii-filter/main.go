// pii-filter is a stateful webhook sidecar for the inference-gateway.
//
// It implements three endpoints that the inference-gateway calls via its
// WebhookHooks mechanism:
//
//	POST /pre-request        — anonymizes PII in inbound messages using Presidio
//	POST /response-transform — de-anonymizes tokens in the LLM response (non-streaming)
//	POST /stream             — per-chunk de-anonymization over a persistent HTTP/2 stream
//
// Per-request state (token→original mapping) is stored in memory, keyed by
// the request_id provided by the inference-gateway. Entries are evicted after
// a configurable TTL (default 5 minutes) to prevent unbounded growth.
//
// Configuration is via environment variables:
//
//	PII_LISTEN              listen address (default ":3100")
//	PII_PRESIDIO_URL        Presidio Analyzer base URL (default "http://presidio-analyzer:3000")
//	PII_LANGUAGE            language code for analysis (default "en")
//	PII_SCORE_THRESHOLD     minimum confidence score, 0–1 (default "0.5")
//	PII_ENTITIES            comma-separated entity type allowlist (default: all)
//	PII_TTL_SECONDS         seconds to retain per-request state (default "300")
//	PII_SKIP_SYSTEM_MSGS    skip anonymization of system messages ("true"/"false", default "false")
package main

import (
	"bufio"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/presidio"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	listen := envOr("PII_LISTEN", ":3100")
	presidioURL := envOr("PII_PRESIDIO_URL", "http://presidio-analyzer:3000")
	language := envOr("PII_LANGUAGE", "en")
	skipSystem := os.Getenv("PII_SKIP_SYSTEM_MSGS") == "true"
	ttl := time.Duration(envInt("PII_TTL_SECONDS", 300)) * time.Second

	var entities []string
	if raw := os.Getenv("PII_ENTITIES"); raw != "" {
		for _, e := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(e); t != "" {
				entities = append(entities, t)
			}
		}
	}

	threshold := 0.5
	if raw := os.Getenv("PII_SCORE_THRESHOLD"); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			threshold = f
		}
	}

	client := presidio.NewClient(presidio.ClientConfig{
		AnalyzerURL:    presidioURL,
		Language:       language,
		Entities:       entities,
		ScoreThreshold: threshold,
	})

	s := &server{
		client:     client,
		skipSystem: skipSystem,
		ttl:        ttl,
		store:      make(map[string]storeEntry),
	}
	go s.evictLoop()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /pre-request", s.handlePreRequest)
	mux.HandleFunc("POST /response-transform", s.handleResponseTransform)
	mux.HandleFunc("POST /stream", s.handleStream)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	h2s := &http2.Server{}
	log.Printf("pii-filter listening on %s (presidio: %s)", listen, presidioURL)
	if err := http.ListenAndServe(listen, h2c.NewHandler(mux, h2s)); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// ─── wire types (mirror inferencegateway.Webhook* structs) ────────────────

type webhookPreRequest struct {
	RequestID string           `json:"request_id"`
	Model     string           `json:"model"`
	Messages  []webhookMessage `json:"messages"`
}

type webhookMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type webhookPreResponse struct {
	Messages []webhookMessage `json:"messages"`
}

type webhookTransformRequest struct {
	RequestID string `json:"request_id"`
	Model     string `json:"model"`
	Content   string `json:"content"`
}

type webhookTransformResponse struct {
	Content string `json:"content"`
}

// ─── server ───────────────────────────────────────────────────────────────

type storeEntry struct {
	reverseMap     map[string]string
	inputRecords   []presidio.AnonymizationRecord // what was anonymized in the input
	expiresAt      time.Time
}

type server struct {
	client     *presidio.Client
	skipSystem bool
	ttl        time.Duration

	mu    sync.Mutex
	store map[string]storeEntry
}

func (s *server) handlePreRequest(w http.ResponseWriter, r *http.Request) {
	var req webhookPreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	combined := make(map[string]string)
	resp := webhookPreResponse{Messages: make([]webhookMessage, len(req.Messages))}

	for i, msg := range req.Messages {
		resp.Messages[i] = msg // copy role unchanged

		if s.skipSystem && msg.Role == "system" {
			continue
		}
		if msg.Role != "user" && msg.Role != "system" {
			continue
		}
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}

		entities, err := s.client.Analyze(r.Context(), msg.Content)
		if err != nil {
			// Presidio may still be warming up; pass through without anonymization.
			log.Printf("presidio analyze unavailable (req %s), passing through: %v", req.RequestID, err)
			continue
		}
		if len(entities) == 0 {
			continue
		}

		anonymized, reverseMap := presidio.Anonymize(msg.Content, entities)
		resp.Messages[i].Content = anonymized
		presidio.MergeReverseMaps(combined, reverseMap)
	}

	if len(combined) > 0 {
		s.mu.Lock()
		s.store[req.RequestID] = storeEntry{
			reverseMap:   combined,
			inputRecords: presidio.RecordsFromReverseMap(combined),
			expiresAt:    time.Now().Add(s.ttl),
		}
		s.mu.Unlock()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *server) handleResponseTransform(w http.ResponseWriter, r *http.Request) {
	var req webhookTransformRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	content := req.Content

	s.mu.Lock()
	entry, ok := s.store[req.RequestID]
	if ok {
		delete(s.store, req.RequestID)
	}
	s.mu.Unlock()

	if ok && len(entry.reverseMap) > 0 {
		content = presidio.Deanonymize(content, entry.reverseMap)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(webhookTransformResponse{Content: content})
}

// ─── streaming sidecar protocol ───────────────────────────────────────────
//
// Mirrors inferencegateway.SidecarInFrame / SidecarOutFrame.

type sidecarInFrame struct {
	Type      string `json:"type"` // "chunk" or "done"
	RequestID string `json:"request_id"`
	Content   string `json:"content,omitempty"`
	Thinking  string `json:"thinking,omitempty"`
}

type sidecarOutFrame struct {
	Type     string `json:"type"` // "ack", "done", or "error"
	Content  string `json:"content,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	Error    string `json:"error,omitempty"`

	// Anonymization metadata (omitted when empty).
	// InputAnonymizations is populated on the first ack frame for a request.
	// OutputAnonymizations lists tokens substituted back in this chunk.
	InputAnonymizations  []presidio.AnonymizationRecord `json:"input_anonymizations,omitempty"`
	OutputAnonymizations []presidio.AnonymizationRecord `json:"output_anonymizations,omitempty"`
}

// handleStream implements POST /stream using the NDJSON sidecar protocol.
// The gateway opens one persistent HTTP/2 POST per LLM request. We read
// SidecarInFrames from the request body and write SidecarOutFrames to the
// response body, maintaining a 1-in/1-out cadence.
//
// We buffer content that ends with an unclosed PII token (e.g. «PERSO before
// N_0» arrives in the next chunk) and flush the buffer on the "done" frame.
func (s *server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	enc := json.NewEncoder(w)
	scanner := bufio.NewScanner(r.Body)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	var requestID string
	var partial string // suffix that starts an unclosed token
	var inputSent bool // true after inputAnonymizations have been emitted once

	send := func(f sidecarOutFrame) {
		enc.Encode(f) //nolint:errcheck
		flusher.Flush()
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var in sidecarInFrame
		if err := json.Unmarshal(line, &in); err != nil {
			send(sidecarOutFrame{Type: "error", Error: "decode: " + err.Error()})
			return
		}
		if in.RequestID != "" {
			requestID = in.RequestID
		}

		if in.Type == "done" {
			// Flush any buffered partial token — stream is over so it must be literal.
			remaining := partial
			partial = ""
			s.mu.Lock()
			entry, ok := s.store[requestID]
			if ok {
				delete(s.store, requestID)
			}
			s.mu.Unlock()
			var outputRecords []presidio.AnonymizationRecord
			if ok && len(entry.reverseMap) > 0 {
				remaining, outputRecords = presidio.DeanonymizeWithAudit(remaining, entry.reverseMap)
			}
			send(sidecarOutFrame{Type: "done", Content: remaining, OutputAnonymizations: outputRecords})
			return
		}

		// "chunk": accumulate with any carry-over from the previous chunk.
		combined := partial + in.Content
		safe, newPartial := splitAtPartialToken(combined)
		partial = newPartial

		s.mu.Lock()
		entry, ok := s.store[requestID]
		s.mu.Unlock()

		var outputRecords []presidio.AnonymizationRecord
		if ok && len(entry.reverseMap) > 0 {
			safe, outputRecords = presidio.DeanonymizeWithAudit(safe, entry.reverseMap)
		}

		frame := sidecarOutFrame{Type: "ack", Content: safe, Thinking: in.Thinking, OutputAnonymizations: outputRecords}
		// Attach input anonymizations once on the first ack for this request.
		if !inputSent && ok && len(entry.inputRecords) > 0 {
			frame.InputAnonymizations = entry.inputRecords
			inputSent = true
		}
		// Thinking content is not anonymized; pass through unchanged.
		send(frame)
	}
	if err := scanner.Err(); err != nil {
		log.Printf("pii-filter /stream scanner error (req %s): %v", requestID, err)
	}
}

// splitAtPartialToken returns the safe prefix and the held-back suffix.
// We hold back everything from the last unclosed «, because a PII token like
// «PERSON_0» might be split across two chunks at the « boundary or mid-name.
func splitAtPartialToken(s string) (safe, partial string) {
	idx := strings.LastIndex(s, "«")
	if idx == -1 {
		return s, ""
	}
	// If the « is closed before the end of s, the whole string is safe.
	if strings.Contains(s[idx:], "»") {
		return s, ""
	}
	return s[:idx], s[idx:]
}

// ─── eviction ──────────────────────────────────────────────────────────────

// evictLoop removes store entries past their TTL every minute.
func (s *server) evictLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for id, entry := range s.store {
			if now.After(entry.expiresAt) {
				delete(s.store, id)
			}
		}
		s.mu.Unlock()
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
