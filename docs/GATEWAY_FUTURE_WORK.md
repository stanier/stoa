# Gateway Future Work

Features deferred from the initial gateway implementation (Phases 1–4 + rate limiting,
dynamic routing, usage UI). Each item below requires a decision before implementation can
begin.

---

## Semantic Caching

**What**: Cache responses based on semantic similarity of the prompt rather than exact hash
match. A new request that is semantically equivalent to a cached one (e.g. "what's the
capital of France?" vs "tell me the capital of France") returns the cached response.

**Decision needed**: Requires an embedding model call per request to compute a similarity
vector. Pick a provider (e.g. `text-embedding-3-small` on OpenAI, or a locally-hosted model
via Ollama) and accept the added latency (~50–200ms) and cost per cache lookup. Also need to
decide on a similarity threshold — too low causes false hits, too high gives no benefit over
exact match.

**Infrastructure**: A vector store (pgvector extension on the existing agentdb Postgres, or
a dedicated Qdrant/Chroma instance).

---

## Redis Shared Cache

**What**: Replace the in-process `MemoryCache` with a Redis-backed cache so multiple agentd
replicas share the same response cache. The `RedisCache` backend is already stubbed in config
(`GATEWAY_CACHE_REDIS_URL`) and the `Cache` interface is defined — implementation is just
filling in `redis.go`.

**Decision needed**: Only useful when running multiple agentd replicas. Single-instance
deployments get full benefit from the memory cache and should not add Redis complexity.

**Infrastructure**: One Redis container / managed Redis instance.

---

## Guardrails

**What**: Inspect and optionally block/modify LLM inputs and outputs based on policy rules.
Common categories:
- **PII detection and masking** — detect and redact emails, phone numbers, SSNs, etc. before
  sending to the LLM or returning to the user
- **Content moderation** — block hate speech, CSAM, self-harm content
- **Prompt injection detection** — detect attempts to override the system prompt
- **Allow/deny lists** — restrict topics, domains, or instruction patterns

**Decision needed**:
1. Build in-house (regex + heuristics for PII, ML classifier for moderation) vs. call an
   external API (AWS Comprehend, OpenAI moderation endpoint, Lakera Guard)?
2. On violation: block silently, return error to user, or log + pass through?
3. Apply guardrails symmetrically (input + output) or input-only?

**Infrastructure**: Depends on approach. In-house: no new infra. External API: latency +
cost per request. Self-hosted classifier: GPU or CPU inference container.

---

## Cost-Aware Routing

**What**: Route requests to a cheaper model when a quality threshold can be met — e.g. use
`gpt-4o-mini` for simple factual queries and `gpt-4o` for complex reasoning. Reduces spend
without sacrificing quality for easy tasks.

**Decision needed**: Routing signal. Options:
- **Rule-based**: user or app tags the request with a complexity hint
- **Classifier call**: a fast, cheap model classifies the query before routing (adds ~100ms)
- **Heuristic**: token count, presence of code blocks, etc.

Also need to define "quality threshold" — hard to measure objectively without human feedback.

---

## Semantic Routing

**What**: Route requests to a specialised model based on query topic — e.g. send coding
questions to a code-optimised model, medical questions to a fine-tuned clinical model.

**Decision needed**: Requires either a topic classifier (same tradeoffs as cost-aware routing)
or explicit user/app tagging. Need to define the routing taxonomy and which models map to
which topics.

---

## Provider Breadth: AWS Bedrock + GCP Vertex AI

**What**: Native SDK integration for AWS Bedrock and GCP Vertex AI. These providers use
proprietary authentication (AWS SigV4, GCP service accounts) and non-OpenAI-compatible
streaming protocols, so the existing `openai.go` client cannot be reused.

**Decision needed**: Do you actively use Bedrock or Vertex? If yes, which models (Claude on
Bedrock, Gemini on Vertex, Llama on Bedrock)? Note: Groq, Together, Mistral, Fireworks,
Ollama, and most other providers already work via the OpenAI-compat client.

**Infrastructure**: AWS credentials / GCP service account; no new containers.

---

## MCP Gateway / Agent Traffic Governance

**What**: Inspect, log, and enforce policy on MCP tool call traffic flowing through agentd —
analogous to what Kong and Traefik are building for the agentic layer. Examples:
- Audit log every tool call with parameters and result
- Block specific tools for specific users/sessions
- Rate limit tool calls independently of LLM calls
- Inject context (e.g. user identity) into tool call parameters

**Decision needed**: What threat model are you defending against? Internal misuse (agents
calling tools they shouldn't), external abuse (ACP callers with broad tool access), or both?
The existing permissions system (`internal/app/permissions.go`) already handles approval
flows — this would extend it with policy-as-code and audit trails at the gateway layer.

---

## Streaming Cache

**What**: Cache streaming responses so cache hits also stream (rather than returning the full
response as a single chunk). The current `CachingClient` stores completed responses and
replays them as a synthetic two-chunk stream (content + done), which is detectable by clients
that measure time-to-first-token.

**Decision needed**: Is the current replay behaviour acceptable? It produces correct output
but TTFT on a cache hit is effectively 0ms (all content arrives immediately), which may
confuse clients expecting gradual token delivery. A character-by-character replay with
artificial delay would mimic streaming but adds complexity.
