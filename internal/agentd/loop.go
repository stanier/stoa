package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/permissions"
	"github.com/stanier/stoa/internal/presidio"
)

// handleMessage appends the user's content to the session history and runs the
// agentic loop until a final non-tool response is obtained (or max iterations
// are reached). It calls sendEvent to stream chunks and tool events in real time.
// Returns (finalContent, finalThinking, inputTokens, outputTokens, error).
func handleMessage(
	s *Server,
	sess *Session,
	content string,
	imageParts []chat.ImagePart,
	sendEvent func(agentproto.Event),
) (string, string, int, int, error) {
	logging.Infof("agentd: processing message (len=%d, session %s)", len(content), sess.id)

	// Append user message, including any inlined image parts.
	msg := chat.NewUserMessage(content)
	if len(imageParts) > 0 {
		msg.ImageParts = imageParts
	}
	sess.mu.Lock()
	sess.messages = append(sess.messages, msg)
	sess.mu.Unlock()
	_ = sess.tracer.LogUserMessage(content)

	// Fire title generation before the LLM call so the title appears sooner.
	s.maybeGenerateTitle(sess, sendEvent)

	ctx, cancel := context.WithTimeout(context.Background(), agenticLoopTimeout)
	defer cancel()

	return runAgenticLoop(ctx, s, sess, sendEvent)
}


// runAgenticLoop runs the LLM→tool→LLM agentic loop against sess.messages as-is
// (no message prepend). Used for both normal user turns (via handleMessage) and
// result-injection re-entry (via agentResultForwarder in handlers.go).
func runAgenticLoop(
	ctx context.Context,
	s *Server,
	sess *Session,
	sendEvent func(agentproto.Event),
) (string, string, int, int, error) {
	// Attribute permission prompts raised by tool calls to this session so the
	// router delivers them to this session's client only. A sub-agent's context
	// already carries its parent's ID, so its prompts go to the parent's client.
	if permissions.SessionIDFromContext(ctx) == "" {
		ctx = permissions.WithSessionID(ctx, sess.id)
	}
	var (
		finalResponse  string
		finalThinkingB strings.Builder
		usedTools      bool
	)
	var (
		lastInputTokens   int
		totalOutputTokens int
	)

	for iteration := 0; iteration < maxIterations; iteration++ {
		if iteration > 0 {
			logging.Debugf("agentd: agentic loop iteration %d/%d (session %s)", iteration+1, maxIterations, sess.id)
		}

		// Pre-flight budget check on the first iteration only.
		if iteration == 0 && s.agentDB != nil && sess.userID != "" {
			if ok, reason, _ := s.agentDB.CheckBudget(ctx, sess.userID); !ok {
				return finalResponse, finalThinkingB.String(), lastInputTokens, totalOutputTokens,
					fmt.Errorf("token budget exceeded: %s", reason)
			}
		}

		// Snapshot messages and tool list for this call (do not hold lock during I/O).
		// Before the LLM has called list_tools, only expose list_tools itself so the
		// agent must discover available tools explicitly. After list_tools is called,
		// pass the full filtered tool set.
		sess.mu.Lock()
		// Safe read-only view: all writes to sess.messages happen after
		// StreamMessage returns, under sess.mu, so no copy is needed.
		msgs := sess.messages
		var llmTools []llm.Tool
		if sess.toolsRevealed {
			llmTools = sess.llmCtx.Tools
		} else {
			llmTools = sessionListTool(sess)
		}
		overrides := sess.overrides
		sess.mu.Unlock()

		// Debug: dump message history shape so conversation confusion is diagnosable.
		if logging.IsDebug() {
			logging.Debugf("agentd: turn %d — %d msgs, %d tools (session %s)",
				iteration+1, len(msgs), len(llmTools), sess.id)
			for i, m := range msgs {
				preview := m.Content
				if len(preview) > 120 {
					preview = preview[:120] + "…"
				}
				logging.Debugf("agentd:   msg[%02d] role=%-10s len=%-5d %q",
					i, m.Role, len(m.Content), preview)
			}
		}

		// Inject the per-user stoa-infgw_* key so inference-gateway enforces user-scoped quotas.
		// The key is provisioned by authd at login and passed in via SessionConfig.
		if sess.llmKey != "" {
			if overrides == nil {
				overrides = &llm.Overrides{}
			} else {
				cp := *overrides
				overrides = &cp
			}
			overrides.APIKey = &sess.llmKey
		}

		// Attach per-session overrides so the LLM client picks them up.
		callCtx := llm.WithOverrides(ctx, overrides)

		// Select the LLM client: use the override provider if specified, else default.
		llmClient := s.llmClient
		if overrides != nil && overrides.Provider != nil && s.providerRegistry != nil {
			if c, ok := s.providerRegistry.GetClient(*overrides.Provider); ok {
				llmClient = c
			} else {
				logging.Warnf("agentd: provider %q not found, falling back to default", *overrides.Provider)
			}
		}

		s.runPreLLM(ctx, sess.id, msgs)
		provider := effectiveProvider(s, overrides)
		llmStart := time.Now()
		responseChan, err := llmClient.StreamMessage(callCtx, msgs, llmTools)
		if err != nil {
			logging.Errorf("agentd: LLM stream error (session %s): %v", sess.id, err)
			s.metrics.llmRequestsTotal.WithLabelValues(provider, "error").Inc()
			return finalResponse, finalThinkingB.String(), lastInputTokens, totalOutputTokens, fmt.Errorf("LLM stream error: %w", err)
		}

		var (
			iterContent          strings.Builder
			iterThinking         strings.Builder
			iterOutputTokens     int
			toolUse              *llm.ToolUse
			inputAnonymizations  []presidio.AnonymizationRecord
			outputAnonymizations []presidio.AnonymizationRecord
		)

		for chunk := range responseChan {
			if chunk.Error != nil {
				return finalResponse, finalThinkingB.String(), lastInputTokens, totalOutputTokens, fmt.Errorf("stream error: %w", chunk.Error)
			}
			// Collect anonymization metadata delivered on the Done chunk.
			if chunk.Done {
				if len(chunk.InputAnonymizations) > 0 && inputAnonymizations == nil {
					inputAnonymizations = chunk.InputAnonymizations
				}
				outputAnonymizations = append(outputAnonymizations, chunk.OutputAnonymizations...)
			}

			if chunk.Content != "" {
				iterContent.WriteString(chunk.Content)
				sendEvent(agentproto.Event{
					Type:  agentproto.EventTypeChunk,
					Chunk: &agentproto.ChunkEvent{Content: chunk.Content},
				})
			}
			if chunk.Thinking != "" {
				iterThinking.WriteString(chunk.Thinking)
				sendEvent(agentproto.Event{
					Type:  agentproto.EventTypeChunk,
					Chunk: &agentproto.ChunkEvent{Thinking: chunk.Thinking},
				})
			}
			if chunk.ToolUse != nil {
				toolUse = chunk.ToolUse
			}
			if chunk.Done {
				lastInputTokens = chunk.InputTokens
				iterOutputTokens = chunk.OutputTokens
				totalOutputTokens += chunk.OutputTokens
				if s.agentDB != nil && sess.userID != "" && chunk.InputTokens > 0 {
					providerName := "default"
					if sess.overrides != nil && sess.overrides.Provider != nil {
						providerName = *sess.overrides.Provider
					}
					_ = s.agentDB.RecordTokens(ctx, sess.userID, sess.id, providerName, "", chunk.InputTokens, chunk.OutputTokens)
				}
			}
		}

		// Emit anonymization event when the sidecar reported any activity.
		if len(inputAnonymizations) > 0 || len(outputAnonymizations) > 0 {
			sendEvent(agentproto.Event{
				Type: agentproto.EventTypeAnonymization,
				Anonymization: &agentproto.AnonymizationEvent{
					InputAnonymizations:  inputAnonymizations,
					OutputAnonymizations: outputAnonymizations,
				},
			})
		}
		s.metrics.llmRequestDuration.WithLabelValues(provider).Observe(time.Since(llmStart).Seconds())
		s.metrics.llmRequestsTotal.WithLabelValues(provider, "success").Inc()
		if lastInputTokens > 0 {
			s.metrics.llmInputTokens.WithLabelValues(provider).Add(float64(lastInputTokens))
		}
		if iterOutputTokens > 0 {
			s.metrics.llmOutputTokens.WithLabelValues(provider).Add(float64(iterOutputTokens))
		}
		s.runPostLLM(ctx, sess.id, iterContent.String(), iterThinking.String())

		// Accumulate thinking across iterations.
		if iterThinking.Len() > 0 {
			if finalThinkingB.Len() > 0 {
				finalThinkingB.WriteByte('\n')
			}
			finalThinkingB.WriteString(iterThinking.String())
		}

		// --- Tool use branch ---
		if toolUse != nil {
			usedTools = true

			// Pre-flight permission check so we can mark the ToolInvoked event.
			var requiresPermission bool
			if s.permsSetup != nil {
				var preParams map[string]interface{}
				_ = json.Unmarshal(toolUse.Parameters, &preParams)
				if dec, err := s.permsSetup.Checker.Check(ctx, toolUse.Name, "", preParams); err == nil && dec.RequiresPrompt {
					requiresPermission = true
				}
			}

			sendEvent(agentproto.Event{
				Type: agentproto.EventTypeToolInvoked,
				ToolInvoked: &agentproto.ToolInvokedEvent{
					ToolName:           toolUse.Name,
					ToolUseID:          toolUse.ID,
					Params:             toolUse.Parameters,
					RequiresPermission: requiresPermission,
				},
			})

			var toolParams map[string]interface{}
			_ = json.Unmarshal(toolUse.Parameters, &toolParams)
			_ = sess.tracer.LogToolCall(toolUse.Name, toolUse.ID, toolParams)

			// Once the LLM calls list_tools, reveal the full tool set for subsequent iterations.
		if toolUse.Name == "list_tools" {
			sess.mu.Lock()
			sess.toolsRevealed = true
			sess.mu.Unlock()
		}

		s.runPreTool(ctx, sess.id, toolUse.Name, toolUse.ID, toolUse.Parameters)
		toolResult, shouldContinue, fatalErr := executeToolCall(ctx, s, sess, toolUse, iteration)
			s.runPostTool(ctx, sess.id, toolUse.Name, toolUse.ID, toolResult, fatalErr)
			if fatalErr != nil {
				return finalResponse, finalThinkingB.String(), lastInputTokens, totalOutputTokens, fatalErr
			}
			_ = sess.tracer.LogToolResult(toolUse.Name, toolUse.ID, toolResult)

			sendEvent(agentproto.Event{
				Type: agentproto.EventTypeToolResult,
				ToolResult: &agentproto.ToolResultEvent{
					ToolName:  toolUse.Name,
					ToolUseID: toolUse.ID,
					Result:    toolResult,
					IsError:   !shouldContinue,
				},
			})

			if shouldContinue {
				continue
			}
		}

		// --- Final response branch ---
		if iterContent.Len() > 0 {
			finalResponse = iterContent.String()
			_ = usedTools // tool summary could be appended here if desired
			break
		}

		// Empty response without tool calls.
		logging.Warnf("agentd: empty LLM response at iteration %d (session %s)", iteration+1, sess.id)
		break
	}

	finalThinking := finalThinkingB.String()

	// Append the assistant message to history.
	if finalResponse != "" || finalThinking != "" {
		assistantMsg := chat.NewAssistantMessage(finalResponse)
		assistantMsg.Thinking = finalThinking
		sess.mu.Lock()
		sess.messages = append(sess.messages, assistantMsg)
		sess.mu.Unlock()
		_ = sess.tracer.LogAssistantMessage(finalResponse, finalThinking)
	}

	// Update session token count before auto-compact so the reset inside
	// maybeAutoCompact isn't clobbered by the caller.
	sess.mu.Lock()
	sess.lastInputTokens = lastInputTokens
	sess.mu.Unlock()

	// Auto-compact if token threshold exceeded. Use total (input+output) to match
	// what is shown to users via \!tokens.
	s.maybeAutoCompact(ctx, sess, lastInputTokens+totalOutputTokens, sendEvent)

	// Persist session state after each turn (and after any auto-compaction).
	s.persistSession(sess)

	return finalResponse, finalThinking, lastInputTokens, totalOutputTokens, nil
}

// effectiveProvider returns the provider name to use for metric labels.
// Prefers the per-session override, falls back to the config default.
func effectiveProvider(s *Server, overrides *llm.Overrides) string {
	if overrides != nil && overrides.Provider != nil {
		return *overrides.Provider
	}
	if dp := s.cfg.LLM.DefaultProvider(); dp != nil {
		return dp.Name
	}
	return "unknown"
}

// sessionListTool returns the list_tools tool as an llm.Tool slice for use as the
// initial restricted tool set. Called with sess.mu held.
func sessionListTool(sess *Session) []llm.Tool {
	lt, ok := sess.perSessionTools["list_tools"]
	if !ok {
		return nil
	}
	return []llm.Tool{{
		Name:        lt.Name(),
		Description: lt.Description(),
		InputSchema: lt.Schema(),
	}}
}

// toolAllowedForSession reports whether sess may execute the named tool.
// A tool is allowed when it is in the session's filtered tool list
// (sess.llmCtx.Tools). Per-session tools with no global counterpart
// (list_tools, read_tool_result, wait_for_message) are session plumbing and
// always allowed; per-session tools that shadow a global stub (spawn_agent and
// friends) are subject to the same filter as the stub.
func toolAllowedForSession(s *Server, sess *Session, name string) bool {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	for _, t := range sess.llmCtx.Tools {
		if t.Name == name {
			return true
		}
	}
	if _, perSession := sess.perSessionTools[name]; perSession {
		if _, global := s.skillRegistry.GetTool(name); !global {
			return true
		}
	}
	return false
}

// executeToolCall runs a single tool and appends the result as a user message.
// Per-session tools (e.g. spawn_agent) are checked before the global skill registry
// so that real implementations shadow stubs.
// Returns (resultText, shouldContinue, fatalError).
func executeToolCall(
	ctx context.Context,
	s *Server,
	sess *Session,
	toolUse *llm.ToolUse,
	iteration int,
) (string, bool, error) {
	logging.Infof("agentd: tool call %q (iteration %d, session %s)", toolUse.Name, iteration+1, sess.id)
	toolStart := time.Now()

	// Parse parameters for display and registry.
	var params map[string]interface{}
	if err := json.Unmarshal(toolUse.Parameters, &params); err != nil {
		logging.Warnf("agentd: failed to unmarshal tool params for %s: %v", toolUse.Name, err)
		params = map[string]interface{}{"error": "failed to parse parameters"}
	}

	// Check per-session tools first (sub-agent tools shadow global stubs).
	sess.mu.Lock()
	tool, ok := sess.perSessionTools[toolUse.Name]
	sess.mu.Unlock()
	if !ok {
		tool, ok = s.skillRegistry.GetTool(toolUse.Name)
	}
	if ok && !toolAllowedForSession(s, sess, toolUse.Name) {
		// The model only sees the session's filtered tool list, but it can still
		// emit any tool name (e.g. via prompt injection). Treat a tool outside the
		// session's allowlist as if it did not exist.
		logging.Warnf("agentd: tool %q is not allowed in session %s", toolUse.Name, sess.id)
		ok = false
	}
	if !ok {
		s.metrics.toolCallsTotal.WithLabelValues(toolUse.Name, "not_found").Inc()
		errMsg := fmt.Sprintf("Tool '%s' does not exist. Use only tools listed in your system prompt.", toolUse.Name)
		logging.Warnf("agentd: tool not found: %s", toolUse.Name)

		sess.mu.Lock()
		sess.messages = append(sess.messages, chat.Message{
			Role:    "user",
			Content: fmt.Sprintf("[Tool error for %s]: %s", toolUse.Name, errMsg),
		})
		sess.mu.Unlock()

		return "ERROR: " + errMsg, true, nil
	}

	// Execute the tool.
	result, err := tool.Execute(ctx, json.RawMessage(toolUse.Parameters))
	s.metrics.toolCallDuration.WithLabelValues(toolUse.Name).Observe(time.Since(toolStart).Seconds())
	if err != nil {
		s.metrics.toolCallsTotal.WithLabelValues(toolUse.Name, "error").Inc()
		return "", false, fmt.Errorf("tool execution failed: %w", err)
	}

	if !result.Success {
		s.metrics.toolCallsTotal.WithLabelValues(toolUse.Name, "error").Inc()
		logging.Warnf("agentd: tool %s returned error: %s", toolUse.Name, result.Error)

		sess.mu.Lock()
		sess.messages = append(sess.messages, chat.Message{
			Role:    "user",
			Content: fmt.Sprintf("[Tool error for %s]: %s\nReview your parameters and try again.", toolUse.Name, result.Error),
		})
		sess.mu.Unlock()

		return "ERROR: " + result.Error, true, nil
	}

	s.metrics.toolCallsTotal.WithLabelValues(toolUse.Name, "success").Inc()
	logging.Infof("agentd: tool %s returned %d bytes", toolUse.Name, len(result.Content))

	// Truncate oversized results to protect the LLM context window.
	// Full content is stored in the session cache; the LLM can retrieve
	// subsequent pages via the read_tool_result per-session tool.
	paged := maybePageResult(sess, toolUse.Name, result.Content)

	// Append tool result as user message for next LLM iteration.
	sess.mu.Lock()
	sess.messages = append(sess.messages, chat.Message{
		Role:    "user",
		Content: fmt.Sprintf("[Tool result for %s]: %s", toolUse.Name, paged),
	})
	sess.mu.Unlock()

	return paged, true, nil
}
