# LLM API Hyperparameter Reference

A comprehensive guide to sampling and generation hyperparameters supported by various LLM APIs. Use this as a reference when implementing configurable parameter support in applications.

---

## Summary Table

| Parameter | OpenAI | Anthropic | Llama.cpp | Ollama | HuggingFace | Together.ai | Groq | Cohere |
|-----------|--------|-----------|-----------|--------|-------------|-------------|------|--------|
| **temperature** | ✅ (0-2) | ✅ (0-1) | ✅ (0.8) | ✅ (1.0) | ✅ (1.0) | ✅ | ✅ (0-2) | ✅ (0-1) |
| **top_p** | ✅ | ✅ | ✅ | ✅ (0.9) | ✅ | ✅ | ✅ (0-1) | ✅ (0.01-0.99) |
| **top_k** | ❌ | ❌ | ✅ | ✅ (50) | ✅ | ✅ | ✅ (0-20) | ✅ (0-500) |
| **frequency_penalty** | ✅ (-2.0-2.0) | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **presence_penalty** | ✅ (-2.0-2.0) | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **max_tokens** | ✅ | ✅ (required) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| **repeat_penalty** | ❌ | ❌ | ✅ (1.1) | ❌ | ❌ | ❌ | ❌ | ❌ |
| **typical_p** | ❌ | ❌ | ✅ (1.0) | ❌ | ❌ | ❌ | ❌ | ❌ |
| **dynatemp_range** | ❌ | ❌ | ✅ (0.0) | ❌ | ❌ | ❌ | ❌ | ❌ |

---

## Parameter Descriptions

### Core Sampling Parameters

#### **temperature**
Controls the randomness/creativity of the output.

- **Low values (0.0 - 0.5)**: More deterministic, focused, consistent outputs. Good for analytical tasks.
- **Mid values (0.5 - 1.0)**: Balanced randomness and coherence. Good for general use.
- **High values (1.0 - 2.0)**: More random, creative, diverse outputs. Good for creative writing.

**Key Detail**: Most providers recommend adjusting *either* temperature *or* top_p, not both.

---

#### **top_p** (Nucleus Sampling)
Limits generation to tokens whose cumulative probability is ≥ top_p. Only the "top" most probable tokens are considered until cumulative probability reaches top_p.

- **top_p = 0.9**: Consider tokens until 90% cumulative probability is reached
- **top_p = 0.1**: Restrict to only the most likely tokens (10% probability mass)
- **Typical range**: 0.1 - 1.0

**Interaction with temperature**: Both scale the probability distribution but in different ways. Use one or the other, not both.

---

#### **top_k**
Limits generation to the K most probable tokens at each step. Only the top K tokens by probability are considered.

- **top_k = 50**: Consider only the 50 most likely tokens
- **top_k = 10**: Very restrictive, mostly deterministic
- **Typical range**: 10 - 100 (varies by provider, see table for maximums)

**Use case**: Works well in combination with temperature for fine-grained control.

---

#### **max_tokens**
Maximum number of tokens to generate before stopping.

- **Note**: Model may stop generating before reaching this limit
- **Anthropic**: Required parameter
- **OpenAI**: Optional, defaults to model maximum
- **Ollama**: Specified via `num_predict`

---

### Advanced/Provider-Specific Parameters

#### **frequency_penalty** (OpenAI only)
Range: -2.0 to 2.0 (default: 0.0)

Penalizes tokens proportionally to how often they've appeared in the response so far.

- **Positive values** (e.g., 1.0): Reduce repetition, encourage diverse vocabulary
- **Negative values** (e.g., -1.0): Encourage repetition
- **Use case**: Prevent word repetition and increase variety

---

#### **presence_penalty** (OpenAI only)
Range: -2.0 to 2.0 (default: 0.0)

Penalizes any token that has appeared at least once in the response so far.

- **Positive values** (e.g., 1.0): Encourage the model to talk about new topics
- **Negative values** (e.g., -1.0): Encourage the model to stick to topics
- **Difference from frequency_penalty**: Presence penalty applies once per token, regardless of count

---

#### **repeat_penalty** (Llama.cpp)
Default: 1.1

Controls repetition of token sequences. Higher values penalize repetition more strongly.

- **repeat_last_n**: How many previous tokens to consider for penalizing repetition (default: 64, -1 = context size)

---

#### **typical_p** (Llama.cpp)
Default: 1.0 (disabled)

Enables locally typical sampling. Focuses on tokens that are typologically consistent with preceding tokens.

- **Reduces "crazy" tokens** while maintaining diversity
- **Range**: 0.0 - 1.0

---

#### **dynatemp_range** (Llama.cpp)
Default: 0.0 (disabled)

Dynamic temperature range. Varies temperature within [temperature - dynatemp_range, temperature + dynatemp_range] based on output uncertainty.

- **Use case**: Adaptive randomness based on model confidence

---

---

## API-Specific Details

### OpenAI API
**Primary Source**: [Chat Completions API Reference](https://platform.openai.com/docs/api-reference/chat)

**Supported Parameters**:
- `temperature` (0 - 2, default: 1)
- `top_p` (0 - 1, default: 1)
- `frequency_penalty` (-2 to 2, default: 0)
- `presence_penalty` (-2 to 2, default: 0)
- `max_tokens` (optional)

**Important Notes**:
- Alter temperature *or* top_p, not both
- Default temperature=1, default top_p=1

---

### Anthropic Claude API
**Primary Source**: [Claude API Reference](https://platform.claude.com/docs/en/api/messages/create)

**Supported Parameters**:
- `temperature` (0 - 1, default: 1.0)
- `top_p` (optional, 0 - 1)
- `max_tokens` (required)

**Important Notes**:
- `max_tokens` is required, not optional
- Even with temperature=0, results are not fully deterministic
- Recommend using temperature, not top_p
- Do not send both temperature and top_p (causes errors with newer models)

---

### Llama.cpp Server
**Primary Source**: [llama.cpp Server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)

**Supported Parameters**:
- `temperature` (default: 0.8)
- `top_p`
- `top_k`
- `repeat_penalty` (default: 1.1)
- `repeat_last_n` (default: 64, -1 for context size)
- `typical_p` (default: 1.0, disabled)
- `dynatemp_range` (default: 0.0, disabled)

**Sampler Chain**:
Configurable via `samplers` parameter (default order): `penalties;dry;top_n_sigma;top_k;typ_p;top_p;min_p;xtc;temperature`

---

### Ollama API
**Primary Source**: [Ollama API Documentation](https://docs.ollama.com/api/generate)

**Supported Parameters**:
- `temperature` (default: 1.0)
- `top_k` (default: 50)
- `top_p` (default: 0.9)
- `num_predict` (number of tokens to generate)

**Important Notes**:
- Parameters can also be set in Modelfile
- Uses `options` parameter for advanced model parameters

---

### HuggingFace Inference API
**Primary Source**: [Detailed Parameters](https://huggingface.co/docs/api-inference/en/detailed_parameters)

**Supported Parameters**:
- `temperature` (default: 1.0, requires `do_sample=true` to have effect)
- `top_p` (0 - 1)
- `top_k`

**Important Notes**:
- Must set `do_sample=true` for temperature to take effect
- Temperature = 0 means always take highest score
- Temperature = 1 means regular sampling
- Temperature = 100 approaches uniform probability

---

### Together.ai API
**Primary Source**: [Together.ai Chat Completions](https://docs.together.ai/reference/chat-completions-1)

**Supported Parameters**:
- `temperature`
- `top_p`
- `top_k`

**OpenAI-Compatible**: Uses OpenAI API format with additional parameters

---

### Groq API
**Primary Source**: [Groq API Reference](https://console.groq.com/docs/api-reference)

**Supported Parameters**:
- `temperature` (0 - 2)
- `top_p` (0 - 1)
- `top_k` (0 - 20, integer)

**Important Notes**:
- Recommend altering temperature *or* top_p, not both
- top_k is an integer (number of tokens)

---

### Cohere API
**Primary Source**: [Advanced Generation Hyperparameters](https://docs.cohere.com/v2/docs/advanced-generation-hyperparameters)

**Supported Parameters**:
- `temperature` (0 - 1, default: 0.3)
- `top_k` (0 - 500, default: 0 = disabled)
- `top_p` (0.01 - 0.99, default: 0.75)

**Important Notes**:
- Default temperature is 0.3 (lower than most providers)
- If both k and p are enabled, p acts after k
- Recommended starting points: temperature=0.3 or 0.5

---

## Recommended Implementation Strategy

### For Universal Support (8/8 providers)
- `temperature`: Core parameter supported everywhere
- `top_p`: Widely supported (7/8, only not in basic Anthropic, but in their top_p)
- `max_tokens`: Universal parameter

### For Intermediate Support (4-6 providers)
- `top_k`: Supported by 5/8 (not OpenAI or Anthropic)
- `frequency_penalty`/`presence_penalty`: OpenAI only

### For Advanced Control (specialized use)
- `repeat_penalty`, `typical_p`, `dynatemp_range`: Llama.cpp specific
- `samplers`: Llama.cpp advanced configuration

### Configuration Best Practices

1. **Core layer** (always include):
   - temperature
   - top_p
   - max_tokens

2. **Extended layer** (if provider supports):
   - top_k
   - frequency_penalty / presence_penalty (OpenAI)
   - repeat_penalty (Llama.cpp)

3. **Advanced layer** (power users):
   - dynatemp_range, typical_p (Llama.cpp)
   - samplers (Llama.cpp)

4. **Validation**:
   - Validate ranges per provider (e.g., Anthropic temperature 0-1 vs OpenAI 0-2)
   - Use sensible defaults (temperature=0.7, top_p=0.9)
   - Warn users if both temperature and top_p are set
   - Skip unsupported parameters per provider

---

## Common Pitfalls

1. **Using both temperature and top_p**: Most providers discourage this. Pick one.
2. **Range confusion**: OpenAI's temperature goes to 2.0, but Anthropic only goes to 1.0
3. **Provider-specific differences**: What's "0.8" for Llama.cpp might not work the same in OpenAI
4. **HuggingFace requires do_sample=true**: Temperature has no effect without it
5. **Anthropic requires max_tokens**: It's not optional like in OpenAI
6. **Llama.cpp sampler chain**: Order matters; fully customizable for advanced users

---

## Configuration Examples

### Conservative (Focused, Deterministic)
```yaml
llm:
  model: gpt-4o
  max_tokens: 4096
  temperature: 0.2
  top_p: 0.1
```

### Balanced (Default-like)
```yaml
llm:
  model: gpt-4o
  max_tokens: 4096
  temperature: 1.0
  top_p: 0.9
```

### Creative (Diverse, Exploratory)
```yaml
llm:
  model: gpt-4o
  max_tokens: 4096
  temperature: 1.5
  top_p: 0.95
  presence_penalty: 0.6
```

### Anthropic with Top-K
```yaml
llm:
  provider: anthropic
  model: claude-sonnet-4-5-20250929
  max_tokens: 4096
  temperature: 1.0
  top_p: 0.9
```

---

## Sources

- [OpenAI Chat Completions](https://platform.openai.com/docs/api-reference/chat)
- [Anthropic Claude API](https://platform.claude.com/docs/en/api/messages/create)
- [Llama.cpp Server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)
- [Ollama API Documentation](https://docs.ollama.com/api/generate)
- [HuggingFace Inference API](https://huggingface.co/docs/api-inference/en/detailed_parameters)
- [Groq API Reference](https://console.groq.com/docs/api-reference)
- [Cohere Advanced Generation Hyperparameters](https://docs.cohere.com/v2/docs/advanced-generation-hyperparameters)
- [Understanding Sampling Parameters](https://www.carneiro.dev/blog/ai/llm-sampling-parameters)
