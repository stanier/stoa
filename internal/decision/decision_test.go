package decision

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Example request and response from Ollama's d1 documentation.
var exampleQuestions = []Question{
	{Name: "urgent", Type: TypeNoul, Instructions: "Is this support request urgent?"},
	{Name: "team", Type: TypeChoice, Instructions: "Which team should handle this request?",
		Criteria: json.RawMessage(`{"billing": "Payments, invoices, and refunds", "technical": "Outages, errors, and configuration", "sales": "Plans and upgrades"}`)},
	{Name: "severity", Type: TypeScore, Instructions: "How severe is the customer impact?",
		Criteria: json.RawMessage(`["No impact", "Minor", "Major", "Critical"]`)},
}

const exampleResponse = `{"model": "d1", "answers": {"urgent": {"type": "noul", "noul": 0.981}, "team": {"type": "choice", "choice": "technical", "probabilities": {"billing": 0.012, "technical": 0.973, "sales": 0.015}, "confidence": 0.96}, "severity": {"type": "score", "score": 2.582, "legend": {"0": "No impact", "1": "Minor", "2": "Major", "3": "Critical"}, "probabilities": {"0": 0.015, "1": 0.012, "2": 0.35, "3": 0.623}, "confidence": 0.582}}, "usage": {"input_tokens": 175, "output_tokens": 0}}`

func TestRequestMarshal_KeepsQuestionOrder(t *testing.T) {
	req := Request{Model: "d1", State: json.RawMessage(`"app crashes"`), Questions: exampleQuestions}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	iu, it, is := strings.Index(s, `"urgent"`), strings.Index(s, `"team"`), strings.Index(s, `"severity"`)
	if iu < 0 || !(iu < it && it < is) {
		t.Fatalf("questions out of order: %s", s)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if back["state"] != "app crashes" || back["model"] != "d1" {
		t.Fatalf("bad top-level fields: %v", back)
	}
	if _, ok := back["images"]; ok {
		t.Fatal("images should be omitted when empty")
	}
	qs := back["questions"].(map[string]any)
	if _, ok := qs["urgent"].(map[string]any)["criteria"]; ok {
		t.Fatal("empty criteria should be omitted")
	}
}

func TestValidate(t *testing.T) {
	ok := Request{Model: "d1", State: json.RawMessage(`{"ticket": 1}`), Questions: exampleQuestions}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	bad := []Request{
		{State: json.RawMessage(`"x"`), Questions: exampleQuestions},
		{Model: "d1", Questions: exampleQuestions},
		{Model: "d1", State: json.RawMessage(`not json`), Questions: exampleQuestions},
		{Model: "d1", State: json.RawMessage(`"x"`)},
		{Model: "d1", State: json.RawMessage(`"x"`), Questions: []Question{exampleQuestions[0], exampleQuestions[0]}},
		{Model: "d1", State: json.RawMessage(`"x"`), Questions: []Question{{Name: "a", Type: "text", Instructions: "?"}}},
		{Model: "d1", State: json.RawMessage(`"x"`), Questions: []Question{{Name: "a", Type: TypeChoice, Instructions: "?", Criteria: json.RawMessage(`{"only": null}`)}}},
		{Model: "d1", State: json.RawMessage(`"x"`), Questions: []Question{{Name: "a", Type: TypeScore, Instructions: "?", Criteria: json.RawMessage(`["one"]`)}}},
		{Model: "d1", State: json.RawMessage(`"x"`), Questions: []Question{{Name: "a", Type: TypeNoul, Instructions: "?", Criteria: json.RawMessage(`{"maybe": "x"}`)}}},
		{Model: "d1", State: json.RawMessage(`"x"`), Questions: []Question{{Name: "a", Type: TypeNoul}}},
	}
	for i, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
}

func TestParseResponse_Example(t *testing.T) {
	res, err := ParseResponse([]byte(exampleResponse), exampleQuestions)
	if err != nil {
		t.Fatal(err)
	}
	if res.Model != "d1" || res.Usage == nil || res.Usage.InputTokens != 175 {
		t.Fatalf("bad header: %+v", res)
	}
	if len(res.Answers) != 3 || res.Answers[0].Name != "urgent" || res.Answers[2].Name != "severity" {
		t.Fatalf("answers out of order: %+v", res.Answers)
	}

	u, _ := res.Answer("urgent")
	if u.Pick != "true" || *u.Value != 0.981 || len(u.Options) != 2 {
		t.Errorf("noul: %+v", u)
	}

	team, _ := res.Answer("team")
	if team.Pick != "technical" || *team.Confidence != 0.96 {
		t.Errorf("choice: %+v", team)
	}
	if got := []string{team.Options[0].Key, team.Options[1].Key, team.Options[2].Key}; strings.Join(got, ",") != "billing,technical,sales" {
		t.Errorf("choice options should follow criteria order, got %v", got)
	}

	sev, _ := res.Answer("severity")
	if sev.Pick != "3" || *sev.Value != 2.582 {
		t.Errorf("score: %+v", sev)
	}
	if sev.Options[0].Label != "No impact" || sev.Options[3].Label != "Critical" {
		t.Errorf("score labels: %+v", sev.Options)
	}
	if len(sev.Raw) == 0 {
		t.Error("raw answer should be kept")
	}
}

func TestParseResponse_WithoutQuestions(t *testing.T) {
	body := `{"model":"d1","answers":{"team":{"type":"choice","probabilities":{"a":0.2,"b":0.7,"c":0.1}},
	"lvl":{"type":"score","score":0.4,"probabilities":{"10":0.1,"2":0.2,"0":0.7}},
	"where":{"type":"box","box":{"top":0.1},"found":0.9}}}`
	res, err := ParseResponse([]byte(body), nil)
	if err != nil {
		t.Fatal(err)
	}
	team, _ := res.Answer("team")
	if team.Pick != "b" || team.Options[0].Key != "b" {
		t.Errorf("choice without question: pick from argmax, sort by probability: %+v", team)
	}
	lvl, _ := res.Answer("lvl")
	if lvl.Options[0].Key != "0" || lvl.Options[1].Key != "2" || lvl.Options[2].Key != "10" || lvl.Pick != "0" {
		t.Errorf("score levels should sort numerically: %+v", lvl.Options)
	}
	box, _ := res.Answer("where")
	if box.Type != "box" || box.Options != nil || len(box.Raw) == 0 {
		t.Errorf("unknown type should pass through raw: %+v", box)
	}
}

func TestParseResponse_Errors(t *testing.T) {
	for _, body := range []string{
		`not json`,
		`{"model":"d1"}`,
		`{"error":"model not loaded"}`,
		`{"error":{"message":"bad"},"answers":{}}`,
		`{"answers":{"a":{"type":"noul"}}}`,
	} {
		if _, err := ParseResponse([]byte(body), nil); err == nil {
			t.Errorf("expected error for %s", body)
		}
	}
}

func TestClient_Decide(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(exampleResponse))
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/v1/systemone", "k1")
	res, err := c.Decide(context.Background(), Request{Model: "d1", State: json.RawMessage(`"crash"`), Questions: exampleQuestions})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer k1" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"questions":{"urgent"`) {
		t.Errorf("body = %s", gotBody)
	}
	if len(res.Answers) != 3 {
		t.Errorf("answers = %d", len(res.Answers))
	}
}

func TestClient_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, "").Decide(context.Background(), Request{Model: "d1", State: json.RawMessage(`"x"`), Questions: exampleQuestions[:1]})
	he, ok := err.(*HTTPError)
	if !ok || he.Status != http.StatusBadRequest {
		t.Fatalf("err = %v", err)
	}
}
