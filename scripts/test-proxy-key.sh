#!/usr/bin/env bash
# test-proxy-key.sh — smoke-test the inference-gateway with an API key using the Anthropic dialect.
#
# Usage:
#   ./scripts/test-proxy-key.sh <api-key> [proxy-url]
#
# Examples:
#   ./scripts/test-proxy-key.sh stoa-infgw_abc123...
#   ./scripts/test-proxy-key.sh stoa-infgw_abc123... http://localhost:8080
#
# The proxy-url defaults to http://localhost:8080.
# The model is taken from PROXY_TEST_MODEL env var, falling back to whatever
# default_model the proxy has configured.

set -euo pipefail

API_KEY="${1:-}"
PROXY_URL="${2:-http://localhost:8080}"
MODEL="${PROXY_TEST_MODEL:-}"

if [[ -z "$API_KEY" ]]; then
  echo "Usage: $0 <api-key> [proxy-url]" >&2
  echo "  API key starts with stoa-infgw_" >&2
  exit 1
fi

echo "→ Proxy:   $PROXY_URL"
echo "→ Key:     ${API_KEY:0:12}... (${#API_KEY} chars)"
echo "→ Model:   ${MODEL:-<proxy default>}"
echo ""

# Build request body.  Model field is omitted when empty so the proxy uses its default.
if [[ -n "$MODEL" ]]; then
  BODY=$(printf '{"model":"%s","max_tokens":64,"messages":[{"role":"user","content":"Reply with exactly: OK"}]}' "$MODEL")
else
  BODY='{"max_tokens":64,"messages":[{"role":"user","content":"Reply with exactly: OK"}]}'
fi

HTTP_CODE=$(curl -s -o /tmp/proxy-test-response.json -w "%{http_code}" \
  -X POST "$PROXY_URL/v1/messages" \
  -H "Content-Type: application/json" \
  -H "x-api-key: $API_KEY" \
  -d "$BODY")

BODY_OUT=$(cat /tmp/proxy-test-response.json)

echo "← HTTP $HTTP_CODE"
echo ""

if [[ "$HTTP_CODE" == "200" ]]; then
  # Extract the text from content[0].text if jq is available, else print raw.
  if command -v jq &>/dev/null; then
    TEXT=$(echo "$BODY_OUT" | jq -r '.content[0].text // .error // "no text field"')
    IN=$(echo "$BODY_OUT"  | jq -r '.usage.input_tokens  // "?"')
    OUT=$(echo "$BODY_OUT" | jq -r '.usage.output_tokens // "?"')
    MODEL_USED=$(echo "$BODY_OUT" | jq -r '.model // "unknown"')
    echo "  Model:   $MODEL_USED"
    echo "  Tokens:  ${IN} in / ${OUT} out"
    echo "  Reply:   $TEXT"
  else
    echo "$BODY_OUT"
  fi
  echo ""
  echo "✓ API key is working."
else
  echo "✗ Request failed."
  echo ""
  if command -v jq &>/dev/null; then
    echo "$BODY_OUT" | jq .
  else
    echo "$BODY_OUT"
  fi
  exit 1
fi
