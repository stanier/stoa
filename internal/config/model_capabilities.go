package config

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Model capabilities beyond ordinary text chat. A model can have several.
const (
	// CapabilityVoice marks a model that holds realtime voice-to-voice
	// conversations (audio in, audio out), e.g. LFM3-Omni on llama.cpp.
	CapabilityVoice = "voice"
	// CapabilityDecision marks a decision model, e.g. Liquid d1: it returns
	// calibrated probabilities over fixed outcomes instead of generated text.
	CapabilityDecision = "decision"
)

// ProviderTypeDecision is a provider that only serves decision models. It
// has no chat endpoint, so agentd registers it without a chat client.
const ProviderTypeDecision = "decision"

var knownCapabilities = map[string]bool{CapabilityVoice: true, CapabilityDecision: true}

// Name-based guesses, used only when model_capabilities has no entry for a
// model. They match the published model names (LiquidAI LFM3-Omni / LFM3-Audio,
// d1, d1-3B, d1-omni-600M, ollama "d1:3b") and nothing else on purpose.
var (
	voiceNameRe    = regexp.MustCompile(`(?i)\blfm[\w.]*-(omni|audio)\b`)
	decisionNameRe = regexp.MustCompile(`(?i)(^|[/\s])d1([-:.]|$)`)
)

// CapabilitiesFor returns the sorted capabilities of a model served by this
// provider. Config entries win over name-based guesses; an exact key wins over
// a "prefix*" key, and the longest prefix wins among prefix keys.
func (pc *LLMProviderConfig) CapabilitiesFor(model string) []string {
	if caps, ok := pc.lookupCapabilities(model); ok {
		return normalizeCapabilities(caps)
	}
	var caps []string
	if pc.Type == ProviderTypeDecision || decisionNameRe.MatchString(model) {
		caps = append(caps, CapabilityDecision)
	}
	if voiceNameRe.MatchString(model) {
		caps = append(caps, CapabilityVoice)
	}
	return normalizeCapabilities(caps)
}

// HasCapability reports whether model has capability cap.
func (pc *LLMProviderConfig) HasCapability(model, cap string) bool {
	for _, c := range pc.CapabilitiesFor(model) {
		if c == cap {
			return true
		}
	}
	return false
}

func (pc *LLMProviderConfig) lookupCapabilities(model string) ([]string, bool) {
	if caps, ok := pc.ModelCapabilities[model]; ok {
		return caps, true
	}
	best, bestLen, found := []string(nil), -1, false
	for key, caps := range pc.ModelCapabilities {
		prefix, isPrefix := strings.CutSuffix(key, "*")
		if isPrefix && strings.HasPrefix(model, prefix) && len(prefix) > bestLen {
			best, bestLen, found = caps, len(prefix), true
		}
	}
	return best, found
}

func normalizeCapabilities(caps []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, c := range caps {
		c = strings.ToLower(strings.TrimSpace(c))
		if knownCapabilities[c] && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// RealtimeEndpoint returns the realtime WebSocket URL for this provider:
// realtime_url when set, otherwise api_url with its scheme switched to ws(s)
// and "/realtime" appended under the /v1 base (llama.cpp serves /v1/realtime).
func (pc *LLMProviderConfig) RealtimeEndpoint() string {
	if pc.RealtimeURL != "" {
		return pc.RealtimeURL
	}
	u := endpointUnderV1(pc.APIURL, "realtime")
	if u == "" {
		return ""
	}
	if after, ok := strings.CutPrefix(u, "https://"); ok {
		return "wss://" + after
	}
	if after, ok := strings.CutPrefix(u, "http://"); ok {
		return "ws://" + after
	}
	return u
}

// DecisionEndpoint returns the decision URL for this provider: decision_url
// when set, otherwise "systemone" under the api_url's /v1 base. That gives
// http://host:11434/v1/systemone for Ollama and
// https://api.liquid.ai/decisions/v1/systemone for Liquid's hosted API when
// api_url is https://api.liquid.ai/decisions/v1.
func (pc *LLMProviderConfig) DecisionEndpoint() string {
	if pc.DecisionURL != "" {
		return pc.DecisionURL
	}
	return endpointUnderV1(pc.APIURL, "systemone")
}

// endpointUnderV1 appends leaf to base, adding "/v1" first unless base
// already ends in it. Returns "" for an unparseable or empty base.
func endpointUnderV1(base, leaf string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}
	if u, err := url.Parse(base); err != nil || u.Host == "" {
		return ""
	}
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base + "/" + leaf
}
