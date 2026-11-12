package decision

import (
	"context"
	"errors"
	"fmt"
)

// Graph is a decision tree: each node asks one question, and the answer's
// outcome picks the edge to the next node. A node with no edge for an
// outcome ends the walk there. Graphs are acyclic for now.
//
// This is the seam for interactive trees and controllable auto-logic later:
// a UI can build a Graph, Walk runs it end to end, and Step lets a caller
// pause between nodes (to show the user, override a pick, or gate on a
// threshold) before going on.
type Graph struct {
	Root  string           `json:"root"`
	Nodes map[string]*Node `json:"nodes"`
}

// Node is one step of a Graph.
type Node struct {
	Question Question `json:"question"`
	// Edges maps an outcome key (see Question.Outcomes) to the next node ID.
	Edges map[string]string `json:"edges,omitempty"`
	// MinConfidence, when set, stops the walk at this node if the model's
	// confidence (or, for noul, the winning probability) is below it, so a
	// person can decide instead.
	MinConfidence *float64 `json:"min_confidence,omitempty"`
}

// MaxGraphNodes bounds graph size, and so the number of model calls a walk makes.
const MaxGraphNodes = 64

// Validate checks that the root exists, every question is valid, every edge
// uses a real outcome and points at a real node, and there are no cycles.
func (g *Graph) Validate() error {
	if len(g.Nodes) == 0 {
		return errors.New("decision: graph has no nodes")
	}
	if len(g.Nodes) > MaxGraphNodes {
		return fmt.Errorf("decision: graph has %d nodes, max %d", len(g.Nodes), MaxGraphNodes)
	}
	if _, ok := g.Nodes[g.Root]; !ok {
		return fmt.Errorf("decision: graph root %q is not a node", g.Root)
	}
	for id, n := range g.Nodes {
		if n == nil {
			return fmt.Errorf("decision: node %q is empty", id)
		}
		if n.Question.Name == "" {
			n.Question.Name = id
		}
		if err := n.Question.Validate(); err != nil {
			return fmt.Errorf("decision: node %q: %w", id, err)
		}
		valid := map[string]bool{}
		for _, o := range n.Question.Outcomes() {
			valid[o] = true
		}
		for outcome, next := range n.Edges {
			if !valid[outcome] {
				return fmt.Errorf("decision: node %q: edge on unknown outcome %q", id, outcome)
			}
			if _, ok := g.Nodes[next]; !ok {
				return fmt.Errorf("decision: node %q: edge %q points at missing node %q", id, outcome, next)
			}
		}
	}
	// Cycle check (DFS with colours).
	const (
		white = iota
		grey
		black
	)
	colour := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		colour[id] = grey
		for _, next := range g.Nodes[id].Edges {
			switch colour[next] {
			case grey:
				return fmt.Errorf("decision: graph has a cycle through %q", next)
			case white:
				if err := visit(next); err != nil {
					return err
				}
			}
		}
		colour[id] = black
		return nil
	}
	for id := range g.Nodes {
		if colour[id] == white {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}

// StepResult is what one node's question resolved to.
type StepResult struct {
	Node   string `json:"node"`
	Answer Answer `json:"answer"`
	// Next is the node the answer leads to; empty when the walk ends here.
	Next string `json:"next,omitempty"`
	// Stopped says why the walk ended at this node: "leaf" (no edge for the
	// outcome) or "low_confidence". Empty when the walk goes on.
	Stopped string `json:"stopped,omitempty"`
}

// Trace is a full walk through a graph.
type Trace struct {
	Steps []StepResult `json:"steps"`
	Usage Usage        `json:"usage"`
}

// Step asks node id's question about state and says where to go next.
func (g *Graph) Step(ctx context.Context, d Decider, model string, state []byte, images []string, id string) (StepResult, *Usage, error) {
	n, ok := g.Nodes[id]
	if !ok {
		return StepResult{}, nil, fmt.Errorf("decision: no node %q", id)
	}
	q := n.Question
	if q.Name == "" {
		q.Name = id
	}
	res, err := d.Decide(ctx, Request{Model: model, State: state, Images: images, Questions: []Question{q}})
	if err != nil {
		return StepResult{}, nil, err
	}
	a, ok := res.Answer(q.Name)
	if !ok {
		return StepResult{}, res.Usage, fmt.Errorf("decision: node %q: model did not answer %q", id, q.Name)
	}
	step := StepResult{Node: id, Answer: *a}
	if n.MinConfidence != nil && confidenceOf(a) < *n.MinConfidence {
		step.Stopped = "low_confidence"
		return step, res.Usage, nil
	}
	if next, ok := n.Edges[a.Pick]; ok {
		step.Next = next
	} else {
		step.Stopped = "leaf"
	}
	return step, res.Usage, nil
}

// Walk runs the graph from its root until a leaf or a low-confidence stop.
// Validate first; Walk also guards against runaway walks.
func (g *Graph) Walk(ctx context.Context, d Decider, model string, state []byte, images []string) (*Trace, error) {
	return g.WalkFrom(ctx, d, model, state, images, "", 0)
}

// WalkFrom runs the graph from node from (the root when empty) for at most
// maxSteps nodes (0 means until the walk ends). A caller stepping through a
// graph one node at a time passes maxSteps 1 and the previous step's Next;
// when a person overrides a pick, it passes the node they chose instead.
// When maxSteps runs out, the last step's Next is set and Stopped is empty.
func (g *Graph) WalkFrom(ctx context.Context, d Decider, model string, state []byte, images []string, from string, maxSteps int) (*Trace, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	if from == "" {
		from = g.Root
	}
	if _, ok := g.Nodes[from]; !ok {
		return nil, fmt.Errorf("decision: start node %q is not a node", from)
	}
	if maxSteps < 0 {
		return nil, errors.New("decision: max steps must not be negative")
	}
	limit := len(g.Nodes)
	if maxSteps > 0 && maxSteps < limit {
		limit = maxSteps
	}
	tr := &Trace{}
	id := from
	for i := 0; i < limit; i++ {
		step, usage, err := g.Step(ctx, d, model, state, images, id)
		if usage != nil {
			tr.Usage.InputTokens += usage.InputTokens
			tr.Usage.OutputTokens += usage.OutputTokens
		}
		if err != nil {
			return tr, err
		}
		tr.Steps = append(tr.Steps, step)
		if step.Next == "" {
			return tr, nil
		}
		id = step.Next
	}
	if maxSteps > 0 && limit == maxSteps {
		return tr, nil // paused; the caller goes on from the last step's Next
	}
	return tr, errors.New("decision: walk exceeded graph size")
}

// confidenceOf is the model's confidence, or the winning probability for
// answers (like noul) that report none.
func confidenceOf(a *Answer) float64 {
	if a.Confidence != nil {
		return *a.Confidence
	}
	best := 0.0
	for _, o := range a.Options {
		if o.Probability > best {
			best = o.Probability
		}
	}
	return best
}
