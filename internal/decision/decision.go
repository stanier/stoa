// Package decision supports decision models such as Liquid AI's d1.
//
// A decision model does not generate text. It reads a state (text, JSON,
// images, audio on omni variants) and answers one or more named questions in
// a single forward pass, returning calibrated probabilities over each
// question's fixed outcomes. The wire format here follows the "System One"
// API documented by Liquid (hosted, POST .../decisions/v1/systemone) and by
// Ollama (POST /v1/systemone):
//
//	request:  {"model", "state", "images"?, "questions": {name: {type, instructions, criteria?}}}
//	response: {"model", "answers": {name: {type, ...}}, "usage": {input_tokens, output_tokens}}
//
// Three question types are supported everywhere (Liquid docs; llama.cpp and
// Ollama support only these): "noul" (yes/no), "choice" (one of named
// options) and "score" (a level on an ordered scale). Other "typed answer"
// kinds that the Transformers runtime offers are passed through untouched in
// Answer.Raw so callers can still show them.
package decision

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Question types.
const (
	TypeNoul   = "noul"
	TypeChoice = "choice"
	TypeScore  = "score"
)

// Limits from the published API: 1 to 64 questions per request, 2 to 10
// score levels.
const (
	MaxQuestions   = 64
	MinScoreLevels = 2
	MaxScoreLevels = 10
)

// Question is one named question. Criteria depends on Type:
//   - noul:   optional {"true": "...", "false": "..."}
//   - choice: {option: description-or-null}, at least two options
//   - score:  ["lowest level", ..., "highest level"], 2 to 10 entries
type Question struct {
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

// Request asks a decision model one or more questions about State.
type Request struct {
	Model string
	// State is a JSON string or a JSON object/array (structured input).
	State json.RawMessage
	// Images are base64 images shared by all questions. Ollama wants raw
	// base64; Liquid's hosted API accepts data: URLs. Passed through as given.
	Images    []string
	Questions []Question
}

// Validate checks the request against the documented limits.
func (r *Request) Validate() error {
	if r.Model == "" {
		return errors.New("decision: model is required")
	}
	if len(bytes.TrimSpace(r.State)) == 0 {
		return errors.New("decision: state is required")
	}
	if !json.Valid(r.State) {
		return errors.New("decision: state must be a JSON string, object or array")
	}
	if len(r.Questions) == 0 || len(r.Questions) > MaxQuestions {
		return fmt.Errorf("decision: need 1 to %d questions, got %d", MaxQuestions, len(r.Questions))
	}
	seen := map[string]bool{}
	for i := range r.Questions {
		q := &r.Questions[i]
		if q.Name == "" {
			return fmt.Errorf("decision: question %d has no name", i)
		}
		if seen[q.Name] {
			return fmt.Errorf("decision: duplicate question name %q", q.Name)
		}
		seen[q.Name] = true
		if err := q.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks one question's type and criteria.
func (q *Question) Validate() error {
	if q.Instructions == "" {
		return fmt.Errorf("decision: question %q has no instructions", q.Name)
	}
	switch q.Type {
	case TypeNoul:
		if len(q.Criteria) > 0 && !isNull(q.Criteria) {
			var c map[string]string
			if err := json.Unmarshal(q.Criteria, &c); err != nil {
				return fmt.Errorf("decision: question %q: noul criteria must be {\"true\": ..., \"false\": ...}", q.Name)
			}
			for k := range c {
				if k != "true" && k != "false" {
					return fmt.Errorf("decision: question %q: noul criteria key %q must be \"true\" or \"false\"", q.Name, k)
				}
			}
		}
	case TypeChoice:
		opts, err := q.ChoiceOptions()
		if err != nil {
			return err
		}
		if len(opts) < 2 {
			return fmt.Errorf("decision: question %q: choice needs at least two options", q.Name)
		}
	case TypeScore:
		levels, err := q.ScoreLevels()
		if err != nil {
			return err
		}
		if len(levels) < MinScoreLevels || len(levels) > MaxScoreLevels {
			return fmt.Errorf("decision: question %q: score needs %d to %d levels, got %d", q.Name, MinScoreLevels, MaxScoreLevels, len(levels))
		}
	default:
		return fmt.Errorf("decision: question %q: unsupported type %q (want noul, choice or score)", q.Name, q.Type)
	}
	return nil
}

// ChoiceOptions returns a choice question's option names in criteria order.
func (q *Question) ChoiceOptions() ([]string, error) {
	keys, err := objectKeys(q.Criteria)
	if err != nil {
		return nil, fmt.Errorf("decision: question %q: choice criteria must be an object of option: description", q.Name)
	}
	return keys, nil
}

// ScoreLevels returns a score question's level descriptions, lowest first.
func (q *Question) ScoreLevels() ([]string, error) {
	var levels []string
	if err := json.Unmarshal(q.Criteria, &levels); err != nil {
		return nil, fmt.Errorf("decision: question %q: score criteria must be an array of level descriptions", q.Name)
	}
	return levels, nil
}

// Outcomes lists the outcome keys a question can resolve to: "true"/"false"
// for noul, option names for choice, level indexes ("0".."n-1") for score.
// Graph edges are keyed by these.
func (q *Question) Outcomes() []string {
	switch q.Type {
	case TypeNoul:
		return []string{"true", "false"}
	case TypeChoice:
		opts, _ := q.ChoiceOptions()
		return opts
	case TypeScore:
		levels, _ := q.ScoreLevels()
		out := make([]string, len(levels))
		for i := range levels {
			out[i] = strconv.Itoa(i)
		}
		return out
	}
	return nil
}

// MarshalJSON writes the wire request. Questions go out as a JSON object in
// slice order, because the API answers in the order the questions were asked.
func (r Request) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(`{"model":`)
	if err := writeJSON(&buf, r.Model); err != nil {
		return nil, err
	}
	buf.WriteString(`,"state":`)
	buf.Write(r.State)
	if len(r.Images) > 0 {
		buf.WriteString(`,"images":`)
		if err := writeJSON(&buf, r.Images); err != nil {
			return nil, err
		}
	}
	buf.WriteString(`,"questions":{`)
	for i, q := range r.Questions {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeJSON(&buf, q.Name); err != nil {
			return nil, err
		}
		buf.WriteByte(':')
		wire := struct {
			Type         string          `json:"type"`
			Instructions string          `json:"instructions"`
			Criteria     json.RawMessage `json:"criteria,omitempty"`
		}{q.Type, q.Instructions, q.Criteria}
		if err := writeJSON(&buf, wire); err != nil {
			return nil, err
		}
	}
	buf.WriteString("}}")
	return buf.Bytes(), nil
}

// Option is one outcome of an answer with its probability.
type Option struct {
	// Key is the outcome key ("true"/"false", the option name, or the score
	// level index), matching Question.Outcomes.
	Key string `json:"key"`
	// Label is what to show: the option name, or the score level's legend.
	Label       string  `json:"label"`
	Probability float64 `json:"probability"`
}

// Answer is one question's answer, normalised across question types.
type Answer struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Pick is the most probable outcome key. For noul it is "true" when
	// P(true) >= 0.5; Liquid says to calibrate real thresholds on your own
	// data, so treat this as a display default, not a decision rule.
	Pick string `json:"pick,omitempty"`
	// Value is P(true) for noul and the probability-weighted level for score.
	Value *float64 `json:"value,omitempty"`
	// Confidence is the model's "how strongly the probabilities favour one
	// answer" figure (choice and score). It is not the chance of being right.
	Confidence *float64 `json:"confidence,omitempty"`
	// Options holds every outcome, in criteria order when the question is
	// known and by descending probability otherwise.
	Options []Option `json:"options,omitempty"`
	// Raw is the answer exactly as the model returned it.
	Raw json.RawMessage `json:"raw"`
}

// Usage is the token usage the API reports. Output is always zero.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Result is a parsed decision response.
type Result struct {
	Model   string   `json:"model"`
	Answers []Answer `json:"answers"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Answer returns the answer to the named question.
func (r *Result) Answer(name string) (*Answer, bool) {
	for i := range r.Answers {
		if r.Answers[i].Name == name {
			return &r.Answers[i], true
		}
	}
	return nil, false
}

// ParseResponse parses a System One response body. questions may be nil;
// when given, it orders options by criteria and fills score labels when the
// model omits a legend.
func ParseResponse(body []byte, questions []Question) (*Result, error) {
	var wire struct {
		Model   string          `json:"model"`
		Answers json.RawMessage `json:"answers"`
		Usage   *Usage          `json:"usage"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("decision: parse response: %w", err)
	}
	if len(wire.Error) > 0 && !isNull(wire.Error) {
		return nil, fmt.Errorf("decision: model returned error: %s", errorText(wire.Error))
	}
	names, err := objectKeys(wire.Answers)
	if err != nil {
		return nil, fmt.Errorf("decision: response has no answers object")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(wire.Answers, &raw); err != nil {
		return nil, fmt.Errorf("decision: parse answers: %w", err)
	}
	byName := map[string]*Question{}
	for i := range questions {
		byName[questions[i].Name] = &questions[i]
	}
	res := &Result{Model: wire.Model, Usage: wire.Usage}
	for _, name := range names {
		a, err := parseAnswer(name, raw[name], byName[name])
		if err != nil {
			return nil, err
		}
		res.Answers = append(res.Answers, a)
	}
	return res, nil
}

func parseAnswer(name string, raw json.RawMessage, q *Question) (Answer, error) {
	var w struct {
		Type          string             `json:"type"`
		Noul          *float64           `json:"noul"`
		Choice        string             `json:"choice"`
		Score         *float64           `json:"score"`
		Legend        map[string]string  `json:"legend"`
		Probabilities map[string]float64 `json:"probabilities"`
		Confidence    *float64           `json:"confidence"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return Answer{}, fmt.Errorf("decision: parse answer %q: %w", name, err)
	}
	a := Answer{Name: name, Type: w.Type, Confidence: w.Confidence, Raw: raw}
	if a.Type == "" && q != nil {
		a.Type = q.Type
	}
	switch a.Type {
	case TypeNoul:
		if w.Noul == nil {
			return Answer{}, fmt.Errorf("decision: answer %q: noul answer has no probability", name)
		}
		p := *w.Noul
		a.Value = &p
		a.Options = []Option{{Key: "true", Label: "yes", Probability: p}, {Key: "false", Label: "no", Probability: math.Round((1-p)*1e9) / 1e9}}
		a.Pick = "false"
		if p >= 0.5 {
			a.Pick = "true"
		}
	case TypeChoice:
		a.Pick = w.Choice
		var order []string
		if q != nil {
			order, _ = q.ChoiceOptions()
		}
		a.Options = optionsFrom(w.Probabilities, order, func(k string) string { return k })
		if a.Pick == "" && len(a.Options) > 0 {
			a.Pick = argmax(a.Options)
		}
	case TypeScore:
		a.Value = w.Score
		legend := w.Legend
		if len(legend) == 0 && q != nil {
			if levels, err := q.ScoreLevels(); err == nil {
				legend = map[string]string{}
				for i, l := range levels {
					legend[strconv.Itoa(i)] = l
				}
			}
		}
		keys := make([]string, 0, len(w.Probabilities))
		for k := range w.Probabilities {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return levelLess(keys[i], keys[j]) })
		a.Options = optionsFrom(w.Probabilities, keys, func(k string) string {
			if l, ok := legend[k]; ok {
				return l
			}
			return k
		})
		if len(a.Options) > 0 {
			a.Pick = argmax(a.Options)
		}
	default:
		// Typed answers (text, number, date, box, ...) and future kinds:
		// keep Raw; nothing to normalise.
	}
	return a, nil
}

// optionsFrom builds options in the given key order, then any keys the
// order didn't name by descending probability.
func optionsFrom(probs map[string]float64, order []string, label func(string) string) []Option {
	out := make([]Option, 0, len(probs))
	used := map[string]bool{}
	for _, k := range order {
		if p, ok := probs[k]; ok {
			out = append(out, Option{Key: k, Label: label(k), Probability: p})
			used[k] = true
		}
	}
	var rest []Option
	for k, p := range probs {
		if !used[k] {
			rest = append(rest, Option{Key: k, Label: label(k), Probability: p})
		}
	}
	sort.Slice(rest, func(i, j int) bool {
		if rest[i].Probability != rest[j].Probability {
			return rest[i].Probability > rest[j].Probability
		}
		return rest[i].Key < rest[j].Key
	})
	return append(out, rest...)
}

func argmax(opts []Option) string {
	best := 0
	for i := range opts {
		if opts[i].Probability > opts[best].Probability {
			best = i
		}
	}
	return opts[best].Key
}

func levelLess(a, b string) bool {
	ai, errA := strconv.Atoi(a)
	bi, errB := strconv.Atoi(b)
	if errA == nil && errB == nil {
		return ai < bi
	}
	return a < b
}

// objectKeys returns a JSON object's keys in document order.
func objectKeys(raw json.RawMessage) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func isNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }

func errorText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &o) == nil && o.Message != "" {
		return o.Message
	}
	return string(raw)
}

func writeJSON(buf *bytes.Buffer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	buf.Write(b)
	return nil
}
