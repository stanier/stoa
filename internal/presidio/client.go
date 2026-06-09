// Package presidio provides an HTTP client for the Presidio Analyzer service
// and local helpers for token-based PII anonymization/de-anonymization.
package presidio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// RecognizedEntity is one PII span returned by the Presidio Analyzer.
type RecognizedEntity struct {
	EntityType string  `json:"entity_type"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Score      float64 `json:"score"`
}

type analyzeRequest struct {
	Text           string   `json:"text"`
	Language       string   `json:"language"`
	Entities       []string `json:"entities,omitempty"`
	ScoreThreshold float64  `json:"score_threshold,omitempty"`
}

// ClientConfig configures a Client.
type ClientConfig struct {
	// AnalyzerURL is the base URL of the Presidio Analyzer service
	// (e.g. "http://presidio-analyzer:3000").
	AnalyzerURL string

	// Language passed to the analyzer (default "en").
	Language string

	// Entities is the allowlist of entity types to detect.
	// Leave empty to detect all supported types.
	Entities []string

	// ScoreThreshold is the minimum confidence score [0,1]. Default 0.5.
	ScoreThreshold float64

	// Timeout for individual HTTP calls (default 10s).
	Timeout time.Duration
}

// Client calls the Presidio Analyzer REST API.
type Client struct {
	analyzerURL    string
	httpClient     *http.Client
	language       string
	entities       []string
	scoreThreshold float64
}

// NewClient constructs a Client from cfg.
func NewClient(cfg ClientConfig) *Client {
	lang := cfg.Language
	if lang == "" {
		lang = "en"
	}
	threshold := cfg.ScoreThreshold
	if threshold == 0 {
		threshold = 0.5
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		analyzerURL:    cfg.AnalyzerURL,
		httpClient:     &http.Client{Timeout: timeout},
		language:       lang,
		entities:       cfg.Entities,
		scoreThreshold: threshold,
	}
}

// Analyze calls the Presidio Analyzer and returns detected PII spans for text.
// Spans are sorted by start offset and deduplicated (overlaps resolved by score).
func (c *Client) Analyze(ctx context.Context, text string) ([]RecognizedEntity, error) {
	body, err := json.Marshal(analyzeRequest{
		Text:           text,
		Language:       c.language,
		Entities:       c.entities,
		ScoreThreshold: c.scoreThreshold,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.analyzerURL+"/analyze", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("presidio analyze: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("presidio analyze: unexpected status %d", resp.StatusCode)
	}

	var entities []RecognizedEntity
	if err := json.NewDecoder(resp.Body).Decode(&entities); err != nil {
		return nil, fmt.Errorf("presidio analyze response: %w", err)
	}

	return deduplicateEntities(entities), nil
}

// Anonymize replaces each PII span in text with a unique opaque token and
// returns the anonymized string plus the reverse map (token → original value).
// Tokens look like «PERSON_0», «EMAIL_ADDRESS_1», etc.
func Anonymize(text string, entities []RecognizedEntity) (anonymized string, reverseMap map[string]string) {
	reverseMap = make(map[string]string)
	if len(entities) == 0 {
		return text, reverseMap
	}

	counters := make(map[string]int)
	var sb strings.Builder
	prev := 0

	for _, e := range entities {
		if e.Start < prev {
			continue
		}
		sb.WriteString(text[prev:e.Start])
		idx := counters[e.EntityType]
		counters[e.EntityType]++
		token := fmt.Sprintf("«%s_%d»", e.EntityType, idx)
		reverseMap[token] = text[e.Start:e.End]
		sb.WriteString(token)
		prev = e.End
	}
	sb.WriteString(text[prev:])
	return sb.String(), reverseMap
}

// AnonymizationRecord describes one PII substitution: the opaque token used in
// the LLM context, the Presidio entity type, and the original value.
type AnonymizationRecord struct {
	Token      string `json:"token"`       // e.g. «PERSON_0»
	EntityType string `json:"entity_type"` // e.g. PERSON
	Original   string `json:"original"`    // e.g. John Smith
}

// RecordsFromReverseMap derives AnonymizationRecords from a reverse map.
// The entity type is parsed from the token format «ENTITY_TYPE_N».
func RecordsFromReverseMap(reverseMap map[string]string) []AnonymizationRecord {
	out := make([]AnonymizationRecord, 0, len(reverseMap))
	for token, original := range reverseMap {
		out = append(out, AnonymizationRecord{
			Token:      token,
			EntityType: entityTypeFromToken(token),
			Original:   original,
		})
	}
	return out
}

// Deanonymize replaces all tokens in text with their originals from reverseMap.
func Deanonymize(text string, reverseMap map[string]string) string {
	for token, original := range reverseMap {
		text = strings.ReplaceAll(text, token, original)
	}
	return text
}

// DeanonymizeWithAudit replaces tokens and also returns the records for every
// substitution that actually occurred in text.
func DeanonymizeWithAudit(text string, reverseMap map[string]string) (string, []AnonymizationRecord) {
	var records []AnonymizationRecord
	for token, original := range reverseMap {
		if strings.Contains(text, token) {
			records = append(records, AnonymizationRecord{
				Token:      token,
				EntityType: entityTypeFromToken(token),
				Original:   original,
			})
			text = strings.ReplaceAll(text, token, original)
		}
	}
	return text, records
}

// entityTypeFromToken parses the entity type out of a token like «PERSON_0».
func entityTypeFromToken(token string) string {
	inner := strings.TrimPrefix(strings.TrimSuffix(token, "»"), "«")
	// Find the last underscore that separates the type from the index digit(s).
	idx := strings.LastIndex(inner, "_")
	if idx > 0 {
		return inner[:idx]
	}
	return inner
}

// MergeReverseMaps merges src into dst in place.
func MergeReverseMaps(dst, src map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}

func deduplicateEntities(entities []RecognizedEntity) []RecognizedEntity {
	if len(entities) == 0 {
		return entities
	}
	sort.Slice(entities, func(i, j int) bool {
		if entities[i].Start != entities[j].Start {
			return entities[i].Start < entities[j].Start
		}
		return entities[i].Score > entities[j].Score
	})

	result := []RecognizedEntity{entities[0]}
	for _, e := range entities[1:] {
		last := &result[len(result)-1]
		if e.Start < last.End {
			if e.Score > last.Score {
				*last = e
			}
			continue
		}
		result = append(result, e)
	}
	return result
}
