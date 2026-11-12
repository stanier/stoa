package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// fakeDecider answers each question with a fixed pick.
type fakeDecider struct {
	picks map[string]string  // question name -> outcome key
	conf  map[string]float64 // question name -> confidence
	calls []string
}

func (f *fakeDecider) Decide(_ context.Context, req Request) (*Result, error) {
	res := &Result{Model: req.Model, Usage: &Usage{InputTokens: 10}}
	for _, q := range req.Questions {
		f.calls = append(f.calls, q.Name)
		pick, ok := f.picks[q.Name]
		if !ok {
			return nil, fmt.Errorf("unexpected question %q", q.Name)
		}
		a := Answer{Name: q.Name, Type: q.Type, Pick: pick}
		if c, ok := f.conf[q.Name]; ok {
			a.Confidence = &c
		}
		res.Answers = append(res.Answers, a)
	}
	return res, nil
}

func triageGraph() *Graph {
	return &Graph{
		Root: "urgent",
		Nodes: map[string]*Node{
			"urgent": {
				Question: Question{Type: TypeNoul, Instructions: "Is it urgent?"},
				Edges:    map[string]string{"true": "team"},
			},
			"team": {
				Question: Question{Type: TypeChoice, Instructions: "Which team?",
					Criteria: json.RawMessage(`{"billing": null, "technical": null}`)},
				Edges: map[string]string{"technical": "severity"},
			},
			"severity": {
				Question: Question{Type: TypeScore, Instructions: "How bad?",
					Criteria: json.RawMessage(`["low", "high"]`)},
			},
		},
	}
}

func TestGraphWalk(t *testing.T) {
	g := triageGraph()
	d := &fakeDecider{picks: map[string]string{"urgent": "true", "team": "technical", "severity": "1"}}
	tr, err := g.Walk(context.Background(), d, "d1", []byte(`"crash"`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Steps) != 3 || tr.Steps[2].Stopped != "leaf" || tr.Steps[0].Next != "team" {
		t.Fatalf("trace = %+v", tr.Steps)
	}
	if tr.Usage.InputTokens != 30 {
		t.Errorf("usage = %+v", tr.Usage)
	}
}

func TestGraphWalk_StopsAtLeafOutcome(t *testing.T) {
	d := &fakeDecider{picks: map[string]string{"urgent": "false"}}
	tr, err := triageGraph().Walk(context.Background(), d, "d1", []byte(`"hi"`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Steps) != 1 || tr.Steps[0].Stopped != "leaf" {
		t.Fatalf("trace = %+v", tr.Steps)
	}
}

func TestGraphWalk_LowConfidence(t *testing.T) {
	g := triageGraph()
	min := 0.8
	g.Nodes["team"].MinConfidence = &min
	d := &fakeDecider{
		picks: map[string]string{"urgent": "true", "team": "technical"},
		conf:  map[string]float64{"team": 0.5},
	}
	tr, err := g.Walk(context.Background(), d, "d1", []byte(`"?"`), nil)
	if err != nil {
		t.Fatal(err)
	}
	last := tr.Steps[len(tr.Steps)-1]
	if last.Node != "team" || last.Stopped != "low_confidence" || last.Next != "" {
		t.Fatalf("last step = %+v", last)
	}
}

func TestGraphValidate(t *testing.T) {
	cases := map[string]func(g *Graph){
		"missing root":     func(g *Graph) { g.Root = "nope" },
		"bad outcome":      func(g *Graph) { g.Nodes["urgent"].Edges["maybe"] = "team" },
		"missing target":   func(g *Graph) { g.Nodes["urgent"].Edges["false"] = "ghost" },
		"cycle":            func(g *Graph) { g.Nodes["severity"].Edges = map[string]string{"0": "urgent"} },
		"invalid question": func(g *Graph) { g.Nodes["team"].Question.Criteria = json.RawMessage(`[]`) },
		"empty":            func(g *Graph) { g.Nodes = nil },
	}
	for name, mutate := range cases {
		g := triageGraph()
		mutate(g)
		if err := g.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if err := triageGraph().Validate(); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}
}
