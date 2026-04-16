package agentd

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/logging"
)

const defaultTitlingPrompt = `You generate concise conversation titles.
Given a conversation, respond with a title of at most 6 words that captures the main topic.
Output only the title — no quotes, no punctuation at the end, no explanation.`

// maybeGenerateTitle fires off an async title generation if titling is enabled
// and there is enough content (at least one user + one assistant turn).
func (s *Server) maybeGenerateTitle(sess *Session) {
	if s.cfg == nil || !s.cfg.Titling.Enabled {
		return
	}
	logging.Infof("agentd: titling: queuing title generation for session %s", sess.id)

	// Snapshot messages quickly.
	sess.mu.Lock()
	msgs := make([]chat.Message, len(sess.messages))
	copy(msgs, sess.messages)
	sess.mu.Unlock()

	// Need at least one user message and one assistant message beyond the system prompt.
	userCount, assistantCount := 0, 0
	for _, m := range msgs {
		switch m.Role {
		case "user":
			userCount++
		case "assistant":
			assistantCount++
		}
	}
	if userCount == 0 || assistantCount == 0 {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		title := s.generateTitle(ctx, msgs)
		if title == "" {
			return
		}

		sess.mu.Lock()
		sess.title = title
		sess.mu.Unlock()

		// Persist to DB if available.
		if s.agentDB != nil {
			if err := s.agentDB.UpdateTitle(ctx, sess.id, title); err != nil {
				logging.Warnf("agentd: titling: persist title for %s: %v", sess.id, err)
			}
		}
		logging.Infof("agentd: titling: session %s → %q", sess.id, title)
	}()
}

// generateTitle calls the configured titling model with the conversation and
// returns a cleaned title string, or "" on failure.
func (s *Server) generateTitle(ctx context.Context, msgs []chat.Message) string {
	cfg := s.cfg.Titling

	// Select client.
	var client llm.Client
	if cfg.Provider != "" && s.providerRegistry != nil {
		if c, ok := s.providerRegistry.GetClient(cfg.Provider); ok {
			client = c
		}
	}
	if client == nil {
		client = s.llmClient
	}
	if client == nil {
		return ""
	}

	// Build the titling prompt: system + condensed conversation.
	systemPrompt := cfg.Prompt
	if systemPrompt == "" {
		systemPrompt = defaultTitlingPrompt
	}

	// Build a condensed view: system prompt + up to the last 6 non-system messages.
	titlingMsgs := []chat.Message{
		{Role: "system", Content: systemPrompt},
	}
	// Only include user and assistant turns — skip tool results, errors, notes,
	// and summaries to keep the payload small and focused on the conversation.
	nonSystem := make([]chat.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		// Skip tool result/error lines injected into user turns by the loop.
		if m.Role == "user" && (strings.HasPrefix(m.Content, "[Tool result for ") || strings.HasPrefix(m.Content, "[Tool error for ")) {
			continue
		}
		nonSystem = append(nonSystem, m)
	}
	const maxContext = 6
	if len(nonSystem) > maxContext {
		nonSystem = nonSystem[len(nonSystem)-maxContext:]
	}
	titlingMsgs = append(titlingMsgs, nonSystem...)

	// Ensure the last message is a user turn — some APIs reject trailing assistant messages.
	if len(titlingMsgs) > 0 && titlingMsgs[len(titlingMsgs)-1].Role == "assistant" {
		titlingMsgs = append(titlingMsgs, chat.Message{
			Role:    "user",
			Content: "Generate a short title for this conversation.",
		})
	}

	// Apply overrides: model (if configured), and always disable thinking so a
	// fast summarisation model doesn't burn tokens on reasoning.
	thinkingEnabled := true
	thinkingBudget := 0
	overrides := &llm.Overrides{
		EnableThinking:       &thinkingEnabled,
		ThinkingBudgetTokens: &thinkingBudget,
	}
	if cfg.Model != "" {
		model := cfg.Model
		overrides.Model = &model
	}
	callCtx := llm.WithOverrides(ctx, overrides)

	logging.Infof("agentd: titling: calling model (provider=%q model=%q msgs=%d)", cfg.Provider, cfg.Model, len(titlingMsgs))
	response, err := client.SendMessage(callCtx, titlingMsgs, nil)
	if err != nil {
		logging.Warnf("agentd: titling: model error: %v", err)
		return ""
	}
	logging.Infof("agentd: titling: raw response: %q", response)

	return cleanTitle(response)
}

// cleanTitle strips surrounding whitespace, quotes, and truncates to 80 chars.
func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	s = strings.TrimSpace(s)
	// Truncate at word boundary if over 80 runes.
	if utf8.RuneCountInString(s) > 80 {
		runes := []rune(s)
		s = string(runes[:80])
		if idx := strings.LastIndex(s, " "); idx > 40 {
			s = s[:idx]
		}
		s = strings.TrimRight(s, " .,;:")
	}
	return s
}
