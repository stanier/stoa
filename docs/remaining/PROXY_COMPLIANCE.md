# LLM Proxy — API Dialect Compliance Checklist

Tracks conformance of the inbound proxy endpoints against the official
OpenAI, Anthropic, and Gemini API specifications.

Priority tiers:
- 🔴 **P1 — Broken**: actively incorrect behaviour that causes client failures
- 🟠 **P2 — High**: commonly-used features; major SDKs will send these
- 🟡 **P3 — Medium**: used by a significant minority of clients
- 🟢 **P4 — Low**: niche, rarely exercised

Status legend: ✅ Done · ❌ Open · 🚧 Partial

---

## OpenAI `POST /v1/chat/completions`

### Request fields

| Priority | Status | Field | Notes |
|----------|--------|-------|-------|
| 🔴 P1 | ✅ | `messages[].content[]` image parts | `image_url` parts (https:// and data: URIs) forwarded via `chat.ImagePart`; `input_audio`/`file` still return 400 |
| 🟠 P2 | ✅ | `tool_choice` | Parsed and forwarded via overrides |
| 🟠 P2 | ✅ | `response_format` | `json_object`/`text` via SDK; `json_schema` via `SetExtraFields` |
| 🟠 P2 | ✅ | `messages[].tool_calls[].index` | Incrementing index on streaming deltas; `toolCallCount` tracked |
| 🟡 P3 | ✅ | `seed` | Forwarded via `params.Seed` |
| 🟡 P3 | ❌ | `n` | Multiple completions; always returns 1 (requires fan-out) |
| 🟡 P3 | ✅ | `parallel_tool_calls` | Forwarded via `params.ParallelToolCalls` |
| 🟡 P3 | ❌ | `stream_options` | Client preference ignored; `include_usage` hardcoded upstream |
| 🟢 P4 | ✅ | `user` | Forwarded via `params.User` |
| 🟢 P4 | ✅ | `logit_bias` | Forwarded via `params.LogitBias` |
| 🟢 P4 | ✅ | `logprobs` / `top_logprobs` | Forwarded via `params.Logprobs` / `params.TopLogprobs` |

### Response fields

| Priority | Status | Field | Notes |
|----------|--------|-------|-------|
| 🟠 P2 | ✅ | `created` | `time.Now().Unix()` |
| 🟠 P2 | ✅ | `id` | `crypto/rand` hex |
| 🟡 P3 | ✅ | `system_fingerprint` | Present as `null` (omitempty); propagated if upstream returns one |
| 🟢 P4 | ✅ | `service_tier` | Always `"default"` |
| 🟢 P4 | 🚧 | `choices[].logprobs` | Field present in response type; populated if upstream returns it |
| 🟢 P4 | ✅ | `choices[].message.refusal` | Present as `null` (omitempty) |

### Streaming

| Priority | Status | Issue | Notes |
|----------|--------|-------|-------|
| 🟠 P2 | ✅ | Tool call delta `index` field | Incrementing per tool call in response |
| 🟡 P3 | ❌ | Initial `content: ""` delta | Some older SDK versions expect an empty opener before content |

### Error responses

| Priority | Status | Issue | Notes |
|----------|--------|-------|-------|
| 🟠 P2 | ✅ | `error.code` / `error.param` / `error.type` | Spec-correct shape |

---

## Anthropic `POST /v1/messages`

### Request fields

| Priority | Status | Field | Notes |
|----------|--------|-------|-------|
| 🔴 P1 | ✅ | `max_tokens` validation | Returns 400 when absent or ≤ 0 |
| 🟠 P2 | ✅ | `tool_choice` | Mapped to `ToolChoiceUnionParam` |
| 🟠 P2 | ✅ | `thinking` | Parsed and mapped to `EnableThinking`/`ThinkingBudgetTokens` |
| 🟠 P2 | ✅ | `anthropic-version` header | Accepted without error |
| 🟠 P2 | ✅ | `anthropic-beta` header | Forwarded via `option.WithHeader` |
| 🟡 P3 | ❌ | `tools[].type` | Computer use (`"computer_20241022"`) and text editor tool not handled |
| 🟡 P3 | ✅ | `tools[].cache_control` | Forwarded via `llm.Tool.CacheControl` → `convertToolsToAnthropic` |
| 🟡 P3 | ✅ | `messages[].content[].cache_control` | Forwarded verbatim via raw-message context; `parseContentBlocks` handles text, tool_use, and tool_result blocks |
| 🟡 P3 | ✅ | `system[].cache_control` | Forwarded verbatim via raw-message context; `rebuildAnthropicSystem` preserves it |
| 🟢 P4 | ✅ | `metadata.user_id` | Forwarded via `anthropic.MetadataParam{UserID: ...}` |

### Response fields

| Priority | Status | Field | Notes |
|----------|--------|-------|-------|
| 🟡 P3 | ✅ | `stop_sequence` | Populated from `acc.StopSequence` on Done chunk; included in response |

### Streaming

| Priority | Status | Issue | Notes |
|----------|--------|-------|-------|
| 🔴 P1 | ✅ | Content block `index` monotonicity | State machine with `openBlock` tracking |
| 🟠 P2 | ✅ | `message_delta` usage | Correct |
| 🟡 P3 | ✅ | `message_start` block opening | Lazy; only `message_start` + `ping` emitted upfront |

### Error responses

| Priority | Status | Issue | Notes |
|----------|--------|-------|-------|
| 🟠 P2 | ✅ | `error.type` values | `invalid_request_error`, `authentication_error`, `overloaded_error`, `api_error` |

---

## Gemini `POST /v1beta/models/{model}:generateContent`

### Request fields

| Priority | Status | Field | Notes |
|----------|--------|-------|-------|
| 🟠 P2 | ✅ | `safetySettings[]` | Forwarded via `llm.WithGeminiOptions` context |
| 🟠 P2 | ✅ | `generationConfig.responseMimeType` | Applied to `geminiGenConfig` |
| 🟠 P2 | ✅ | `generationConfig.responseSchema` | Applied to `geminiGenConfig` |
| 🟡 P3 | 🚧 | `contents[].parts[].inlineData` | Returns 400 with clear error; proxy not yet implemented |
| 🟢 P4 | ❌ | `cachedContent` | Context caching reference |

### Response fields

| Priority | Status | Field | Notes |
|----------|--------|-------|-------|
| 🟡 P3 | 🚧 | `candidates[].safetyRatings[]` | Types defined and deserialized; not yet surfaced through streaming layer |
| 🟡 P3 | 🚧 | `candidates[].citationMetadata` | Types defined and deserialized; not yet surfaced through streaming layer |
| 🟢 P4 | ❌ | `candidates[].groundingMetadata` | Google Search grounding |
| 🟢 P4 | ❌ | `promptFeedback` | Safety feedback on the prompt |

### Error responses

| Priority | Status | Issue | Notes |
|----------|--------|-------|-------|
| 🟠 P2 | ✅ | `error.status` values | `UNAUTHENTICATED`, `UNAVAILABLE`, `INTERNAL`, `INVALID_ARGUMENT` |

---

## Cross-cutting

| Priority | Status | Issue | Affects | Notes |
|----------|--------|-------|---------|-------|
| 🔴 P1 | ✅ | CORS headers | All | `corsMiddleware` + `OPTIONS` 204 preflight |
| 🟠 P2 | ✅ | API key auth | All | `apikeydclient` |
| 🟠 P2 | ✅ | `x-request-id` response header | All | `requestIDMiddleware` via `crypto/rand` |
| 🟡 P3 | ❌ | Rate-limit response headers | OpenAI/Anthropic | Requires threading HTTP headers through streaming layer |
| 🟢 P4 | ✅ | `x-stainless-*` SDK telemetry headers | All | `stripSDKHeaders` middleware strips on inbound |

---

## Progress summary

| Dialect | P1 | P2 | P3 | P4 |
|---------|----|----|----|----|
| OpenAI | 0 open | 0 open | 2 open | 0 open |
| Anthropic | 0 open | 0 open | 0 open (1 partial) | 0 open |
| Gemini | 0 open | 0 open | 1 open (2 partial) | 2 open |
| Cross-cutting | 0 open | 0 open | 1 open | 0 open |

*Last updated: 2026-03-22*
