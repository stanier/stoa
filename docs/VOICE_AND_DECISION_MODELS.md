# Voice and decision models

stoa can talk to two kinds of model that are not ordinary chat models:

- **Voice (omni) models** hold a realtime voice-to-voice conversation, such as
  LiquidAI LFM3-Omni or LFM3-Audio served by `llama-server`.
- **Decision models** such as Liquid AI's d1 answer questions about a
  situation with calibrated probabilities instead of generating text.

Both are bare bones today. This page says what works, what is stubbed, and
where the seams for later work are.

## Configuration

Capabilities are set per model on a provider. A name-based guess covers the
published model names (`LFM3-Omni-*`, `LFM3-Audio-*` → voice; `d1`, `d1-3B`,
`d1:3b`, `d1-omni-600M` → decision). Set `model_capabilities` to add others,
or give an empty list to turn a guess off. Keys ending in `*` match by prefix.

```yaml
llm:
  providers:
    # llama.cpp v0.8.0+ serving an omni model
    - name: llama
      type: openai
      api_url: http://llama:8080/v1      # /v1/realtime is derived from this
      model: LFM3-Omni-2B
      models: [LFM3-Omni-2B]
      # realtime_url: ws://llama:8080/v1/realtime   # set when it differs
      model_capabilities:
        "LFM3-Omni-*": [voice]

    # Ollama serving d1 next to chat models
    - name: ollama
      type: openai
      api_url: http://ollama:11434/v1    # /v1/systemone is derived from this
      model_capabilities:
        "d1*": [decision]

    # Liquid's hosted decision API: decision only, no chat
    - name: liquid
      type: decision                     # api_url defaults to https://api.liquid.ai/decisions/v1
      api_key_env: LIQUID_API_KEY        # the default for type: decision
      models: [d1]
```

A `type: decision` provider has no chat client; do not make it the default
provider. Its credentials default to `LIQUID_API_KEY` / `LIQUID_API_URL`, never
the OpenAI variables.

`GET /models` now returns `capabilities: {model: [..]}` per provider, and the
web UI shows them as badges in the model picker.

## Voice mode

Pick a voice model and press the mic button next to Send.

```
browser ──/api/realtime (WS)──▶ webd ──/realtime (WS)──▶ agentd ──▶ llama-server /v1/realtime
```

- **Protocol to the browser** (`internal/realtime`): JSON text frames.
  Client: `audio` (base64 PCM16), `turn.end`, `text`, `cancel`, `config`.
  Server: `ready` (input/output sample rates), `audio`, `text.delta`,
  `transcript.delta`, `transcript.done`, `response.started`,
  `response.done`, `error`.
- **llama.cpp adapter** (`realtime.LlamaCpp`): translates to llama.cpp's
  OpenAI-Realtime-shaped events (llama.cpp v0.8.0, PR #31269):
  `session.update`, `input_audio_buffer.append` / `commit`,
  `response.create` / `cancel`, `conversation.item.create`; reads
  `session.created`, `response.output_audio.delta`,
  `response.output_text.delta`, input transcription events and
  `response.done`.
- **Turn-taking is client-side.** llama.cpp's server-side voice activity
  detection is not documented, so every turn ends with `commit` +
  `response.create`. The browser uses a small energy VAD in hands-free mode
  (with 300 ms pre-roll; talking over a reply cancels it), or push-to-talk.
- **Sample rates come from the server.** llama.cpp does not resample, so
  the browser resamples mic audio to the rate in `ready`. If the server
  omits rates, stoa assumes 16 kHz in and 24 kHz out (the 16 kHz input is an
  assumption).

Not done yet:

- Voice turns are shown in the chat but **not saved** to the session
  history, and the voice conversation does not see the text history.
- Voice traffic goes straight from agentd to the model server: it **bypasses
  inference-gateway** (quotas, metrics, the PII filter).
- No tools during voice turns (llama.cpp added realtime tool calls in
  PR #31287; not wired).
- Other backends (OpenAI Realtime, a cascaded STT → LLM → TTS pipeline using
  llama.cpp's `POST /tts`) plug in by implementing `realtime.Backend`.

## Decision models

Pick a decision model and the composer changes: describe the situation (text
or JSON) and add questions. Each answer shows the pick, the model's
confidence, and a probability bar per outcome.

- **Wire format** (`internal/decision`): Liquid's "System One" API, served by
  Ollama at `POST /v1/systemone` and by Liquid at
  `POST https://api.liquid.ai/decisions/v1/systemone`:
  `{model, state, images?, questions: {name: {type, instructions, criteria}}}`
  → `{model, answers: {name: {...}}, usage}`.
- **Question types:** `noul` (yes/no, P(true)), `choice` (named options) and
  `score` (2 to 10 ordered levels). Liquid's Transformers runtime has more
  "typed answers" (text, number, date, box…); stoa passes those through as raw
  JSON.
- **Normalised answers:** every answer has `pick`, `options[]` (key, label,
  probability), `value` and `confidence` where they apply, plus `raw`. For
  yes/no, `pick` uses 0.5; Liquid says to calibrate real thresholds on your own
  data.
- **API:** `POST /decisions` (agentd) or `/api/decisions` (webd) with
  `{provider, model, state, images?, questions: [{name, type, instructions, criteria}]}`.

### Decision graphs

`decision.Graph` is a small acyclic graph: each node asks one question, and
the answer's outcome picks the edge to the next node (yes/no keys are
`true`/`false`, choice keys are option names, score keys are level indexes
`"0"`..). A node with no edge for the outcome ends the walk. A node's
`min_confidence` stops the walk there so a person can decide.

- **API:** `POST /decisions` takes a `graph` (`{root, nodes: {id: {question,
  edges?, min_confidence?}}}`) in place of `questions` and returns a `trace`
  of steps (`node`, `answer`, `next`, `stopped`). Optional `from` (start node,
  default the root) and `max_steps` (default 0, meaning until the walk ends)
  let a client step one node at a time, or carry on from a node a person
  picked. In Go this is `Graph.WalkFrom`; `Walk` runs from the root to the end.
- **UI:** user menu → **Decision graphs**. Build steps (question, type,
  options), point each outcome at another step or "end here", and set an
  optional "pause for me below N% confidence". A map draws the graph and
  highlights the path taken. Pick a decision model, describe a situation, then
  **Step** or **Run to the end**. The path lists each answer with its
  probabilities; when the model is unsure the run pauses and you pick the
  branch, and you can take a different branch at the latest step any time.
- **Stubbed:** graphs are saved in the browser (localStorage), not on the
  server, so they don't follow you across browsers. No loops, no images in
  the state, no export/import, and no "auto" rules beyond the confidence floor
  yet.

Liquid documents no chaining API; trees are always client-side calls.

### Open questions

- Model names differ between Liquid's sources (d1-2B vs d1-3B; d1-omni-2B vs
  d1-omni-600M). Model IDs are config values, not hard-coded.
- d1 on llama.cpp: Liquid states day-one support for noul/choice/score, but
  the llama-server endpoint for it is not documented. Point `decision_url` at
  it once known.
- d1-omni takes audio as a decision input; not wired.
