package config

import (
	"reflect"
	"testing"
)

func TestCapabilitiesFor_NameGuesses(t *testing.T) {
	pc := &LLMProviderConfig{Type: "openai"}
	cases := map[string][]string{
		"LiquidAI/LFM3-Omni-2B-GGUF": {CapabilityVoice},
		"lfm3-audio-2b":              {CapabilityVoice},
		"d1":                         {CapabilityDecision},
		"d1:3b":                      {CapabilityDecision},
		"LiquidAI/d1-3B":             {CapabilityDecision},
		"d1-omni-600M":               {CapabilityDecision},
		"d1.1":                       {CapabilityDecision},
		"gpt-4o":                     {},
		"lfm3-2b":                    {},
		"qwen3-d1x":                  {},
	}
	for model, want := range cases {
		if got := pc.CapabilitiesFor(model); !reflect.DeepEqual(got, want) {
			t.Errorf("CapabilitiesFor(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestCapabilitiesFor_ConfigWins(t *testing.T) {
	pc := &LLMProviderConfig{
		Type: "openai",
		ModelCapabilities: map[string][]string{
			"my-omni":   {"Voice", "bogus", "voice"},
			"d1":        {},
			"custom-*":  {"decision"},
			"custom-v*": {"voice"},
		},
	}
	cases := map[string][]string{
		"my-omni":     {CapabilityVoice}, // normalised, unknown dropped, deduped
		"d1":          {},                // explicit empty list turns the guess off
		"custom-a":    {CapabilityDecision},
		"custom-v2":   {CapabilityVoice}, // longest prefix wins
		"LFM3-Omni-2": {CapabilityVoice}, // no entry: falls back to guess
	}
	for model, want := range cases {
		if got := pc.CapabilitiesFor(model); !reflect.DeepEqual(got, want) {
			t.Errorf("CapabilitiesFor(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestCapabilitiesFor_DecisionProviderType(t *testing.T) {
	pc := &LLMProviderConfig{Type: ProviderTypeDecision}
	if !pc.HasCapability("anything", CapabilityDecision) {
		t.Fatal("decision provider models should be decision models")
	}
}

func TestEndpoints(t *testing.T) {
	cases := []struct {
		apiURL, realtime, decision string
	}{
		{"http://llama:8080/v1", "ws://llama:8080/v1/realtime", "http://llama:8080/v1/systemone"},
		{"http://llama:8080/", "ws://llama:8080/v1/realtime", "http://llama:8080/v1/systemone"},
		{"https://api.liquid.ai/decisions/v1", "wss://api.liquid.ai/decisions/v1/realtime", "https://api.liquid.ai/decisions/v1/systemone"},
		{"", "", ""},
		{"not a url", "", ""},
	}
	for _, c := range cases {
		pc := &LLMProviderConfig{APIURL: c.apiURL}
		if got := pc.RealtimeEndpoint(); got != c.realtime {
			t.Errorf("RealtimeEndpoint(%q) = %q, want %q", c.apiURL, got, c.realtime)
		}
		if got := pc.DecisionEndpoint(); got != c.decision {
			t.Errorf("DecisionEndpoint(%q) = %q, want %q", c.apiURL, got, c.decision)
		}
	}
	pc := &LLMProviderConfig{APIURL: "http://x/v1", RealtimeURL: "ws://y/rt", DecisionURL: "http://z/d"}
	if pc.RealtimeEndpoint() != "ws://y/rt" || pc.DecisionEndpoint() != "http://z/d" {
		t.Error("explicit URLs should win")
	}
}
