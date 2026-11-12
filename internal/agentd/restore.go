package agentd

import (
	"context"
	"encoding/json"
	"time"

	"github.com/stanier/stoa/internal/agentdb"
	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
)

// sessionLLMContext builds a session's system prompt and tool list. Creation,
// restore after a restart, branching, tool changes and the MCP rebuild all use
// it, so a session ends up with the same tools whichever path built it.
//
// When persona names a published agent in the registry, that agent's tool list
// applies. The session's own allowedTools narrows the result further; it never
// widens it.
func (s *Server) sessionLLMContext(ctx context.Context, persona string, allowedTools []string) app.LLMContext {
	if s.agentDB != nil && persona != "" {
		if a, err := s.agentDB.GetAgent(ctx, persona); err == nil && a != nil && a.PublishedAt != nil {
			return app.BuildLLMContextFiltered(s.cfg, s.skillRegistry, s.promptReg, s.personaReg,
				a.Name, [][]string{a.AllowedTools, allowedTools}, s.agentSkills...)
		}
	}
	return app.BuildLLMContext(s.cfg, s.skillRegistry, s.promptReg, s.personaReg,
		persona, allowedTools, s.agentSkills...)
}

// storedSession is what a session carries across an agentd restart, from
// Postgres or from a session file. The per-user LLM key is deliberately not
// stored; webd supplies it again when the owner reconnects (see handleStream).
type storedSession struct {
	ID, Persona, Source, UserID, Title string
	AllowedTools                       []string
	ToolsLocked                        bool
	CreatedAt                          time.Time
	LastInputTokens, TotalOutputTokens int
	Overrides                          json.RawMessage
	Messages                           []chat.Message // non-system messages
}

func storedFromDB(ds *agentdb.Session, msgs []chat.Message) storedSession {
	return storedSession{
		ID: ds.ID, Persona: ds.Persona, Source: ds.Source, UserID: ds.UserID, Title: ds.Title,
		AllowedTools: ds.AllowedTools, ToolsLocked: ds.ToolsLocked,
		CreatedAt: ds.CreatedAt, LastInputTokens: ds.LastInputTokens, TotalOutputTokens: ds.TotalOutputTokens,
		Overrides: ds.Overrides, Messages: msgs,
	}
}

func storedFromFile(ps *persistedSession) storedSession {
	return storedSession{
		ID: ps.ID, Persona: ps.Persona, Source: ps.Source, UserID: ps.UserID,
		AllowedTools: ps.AllowedTools, ToolsLocked: ps.ToolsLocked,
		CreatedAt: ps.CreatedAt, LastInputTokens: ps.LastInputTokens, TotalOutputTokens: ps.TotalOutputTokens,
		Messages: ps.Messages,
	}
}

// rebuildSession turns a stored session back into a live one, with the same
// owner, tool limits and per-session tools it had when it was created. It does
// not register the session.
func (s *Server) rebuildSession(ctx context.Context, st storedSession) *Session {
	sess := newSession(st.ID, st.Persona, st.Source, st.UserID, "", st.AllowedTools)
	sess.toolsLocked = st.ToolsLocked
	sess.title = st.Title
	sess.createdAt = st.CreatedAt
	sess.lastInputTokens = st.LastInputTokens
	sess.totalOutputTokens = st.TotalOutputTokens
	sess.tracer = app.SetupTracer(s.cfg, st.ID)
	sess.llmCtx = s.sessionLLMContext(ctx, st.Persona, st.AllowedTools)
	sess.perSessionTools["list_tools"] = newListToolsTool(&sess.mu, &sess.llmCtx)

	if len(st.Overrides) > 0 && string(st.Overrides) != "{}" {
		var o llm.Overrides
		if err := json.Unmarshal(st.Overrides, &o); err == nil {
			sess.overrides = &o
		}
	}

	// Fresh system prompt from current config and tools, then the history.
	if sess.llmCtx.SystemPrompt != "" {
		sess.messages = append(sess.messages, systemMessage(sess.llmCtx.SystemPrompt))
	}
	sess.messages = append(sess.messages, st.Messages...)
	// All loaded messages are already persisted.
	sess.persistedMsgCount = nonSystemCount(sess.messages)
	return sess
}
