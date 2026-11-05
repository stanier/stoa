package llm

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stanier/stoa/internal/config"
)

func TestRegistry_CapabilitiesAndDecisionProvider(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Providers = []config.LLMProviderConfig{
		{Name: "llama", Type: "openai", APIURL: "http://llama:8080/v1", APIKey: "x",
			Models: []string{"LFM3-Omni-2B", "qwen3"}},
		{Name: "liquid", Type: config.ProviderTypeDecision, APIURL: "https://api.liquid.ai/decisions/v1",
			Models: []string{"d1", "d1-ultra"}},
	}
	r := NewProviderRegistry(cfg)

	got := map[string][]string{}
	for _, m := range r.ListAllModels() {
		got[m.Provider+"/"+m.ID] = m.Capabilities
	}
	want := map[string][]string{
		"llama/LFM3-Omni-2B": {"voice"},
		"llama/qwen3":        {},
		"liquid/d1":          {"decision"},
		"liquid/d1-ultra":    {"decision"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities = %v, want %v", got, want)
	}

	c, ok := r.GetClient("liquid")
	if !ok {
		t.Fatal("decision provider should be registered")
	}
	if _, err := c.SendMessage(context.Background(), nil, nil); !errors.Is(err, ErrDecisionOnly) {
		t.Fatalf("chat on decision provider: err = %v", err)
	}
	if ids, _ := c.ListModels(context.Background()); len(ids) != 2 {
		t.Fatalf("decision provider models = %v", ids)
	}

	if pc, ok := r.ProviderConfig("default"); !ok || pc.Name != "llama" {
		t.Fatalf("ProviderConfig(default) = %v", pc)
	}
	if !r.HasModel("liquid", "d1") || r.HasModel("liquid", "gpt-4o") {
		t.Fatal("HasModel wrong")
	}
}
