#!/usr/bin/env bash
# init.sh — cold-start wizard for Stoa
#
# Guides through initial configuration for:
#   1. Standalone TUI  (embedded LLM, no daemon)
#   2. agentd + TUI    (daemon owns LLM; TUI is a thin client)
#   3. Containerized   (full stack via podman-compose)
#
# Usage:
#   ./scripts/init.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Compose command — may be overridden in check_container_prereqs if only
# docker-compose is available.
COMPOSE_CMD="podman-compose"

# Provider fields set by select_provider(), consumed by write_llm_config()
# and the next-steps printers.
SELECTED_PROVIDER=""
SELECTED_MODEL=""
SELECTED_API_KEY_ENV=""
SELECTED_API_URL_ENV=""
SELECTED_API_URL=""

# ─────────────────────────────────────────────────────────────────────────────
# Colour helpers
# ─────────────────────────────────────────────────────────────────────────────

bold()   { printf '\033[1m%s\033[0m'  "$*"; }
green()  { printf '\033[32m%s\033[0m' "$*"; }
yellow() { printf '\033[33m%s\033[0m' "$*"; }
red()    { printf '\033[31m%s\033[0m' "$*"; }
cyan()   { printf '\033[36m%s\033[0m' "$*"; }
dim()    { printf '\033[2m%s\033[0m'  "$*"; }

step() { echo; echo "$(bold "▶ $*")"; }
ok()   { echo "  $(green "✓") $*"; }
warn() { echo "  $(yellow "⚠") $*"; }
info() { echo "  $(dim "$*")"; }
fail() { echo "  $(red "✗") $*"; }
hr()   { echo; printf '%s\n' "$(dim "──────────────────────────────────────────────────────────────────")"; }

# ─────────────────────────────────────────────────────────────────────────────
# Utilities
# ─────────────────────────────────────────────────────────────────────────────

require_cmd() {
    if ! command -v "$1" &>/dev/null; then
        fail "$1 is not installed or not in PATH."
        return 1
    fi
}

# prompt_yn QUESTION [DEFAULT]
# Returns 0 for yes, 1 for no.  DEFAULT is "y" or "n" (default "n").
prompt_yn() {
    local question="$1" default="${2:-n}" hint reply
    [[ "$default" == "y" ]] && hint="[Y/n]" || hint="[y/N]"
    printf "  %s %s " "$question" "$hint"
    read -r reply
    reply="${reply:-$default}"
    [[ "$reply" =~ ^[Yy]$ ]]
}

# set_env_var FILE VAR VALUE
# Replaces "VAR=<anything>" with "VAR=VALUE" in FILE.
set_env_var() {
    local file="$1" var="$2" value="$3"
    sed -i "s|^${var}=.*|${var}=${value}|" "$file"
}

# ─────────────────────────────────────────────────────────────────────────────
# Provider selection — sets the SELECTED_* globals
# ─────────────────────────────────────────────────────────────────────────────

select_provider() {
    echo
    echo "  Select LLM provider:"
    echo "  1) Anthropic   $(dim "(claude-sonnet-4-6 — recommended)")"
    echo "  2) OpenAI      $(dim "(gpt-4o)")"
    echo "  3) Other OpenAI-compatible endpoint"
    echo
    printf "  Choice [1]: "
    local choice
    read -r choice
    choice="${choice:-1}"

    case "$choice" in
        1)
            SELECTED_PROVIDER="anthropic"
            SELECTED_MODEL="claude-sonnet-4-6"
            SELECTED_API_KEY_ENV="ANTHROPIC_API_KEY"
            SELECTED_API_URL_ENV=""
            SELECTED_API_URL=""
            ;;
        2)
            SELECTED_PROVIDER="openai"
            SELECTED_MODEL="gpt-4o"
            SELECTED_API_KEY_ENV="OPENAI_API_KEY"
            SELECTED_API_URL_ENV="OPENAI_API_URL"
            SELECTED_API_URL="https://api.openai.com/v1"
            ;;
        3)
            SELECTED_PROVIDER="openai"
            printf "  Model name: "
            read -r SELECTED_MODEL
            printf "  API key env var name [OPENAI_API_KEY]: "
            read -r SELECTED_API_KEY_ENV
            SELECTED_API_KEY_ENV="${SELECTED_API_KEY_ENV:-OPENAI_API_KEY}"
            printf "  API base URL: "
            read -r SELECTED_API_URL
            SELECTED_API_URL_ENV="OPENAI_API_URL"
            ;;
        *)
            fail "Invalid choice."
            exit 1
            ;;
    esac

    printf "  Model [$(dim "${SELECTED_MODEL}")]: "
    local m
    read -r m
    [[ -n "$m" ]] && SELECTED_MODEL="$m"
}

# ─────────────────────────────────────────────────────────────────────────────
# Write a minimal ~/.config/stoa/config.yaml
# ─────────────────────────────────────────────────────────────────────────────

write_llm_config() {
    local file="$1"
    mkdir -p "$(dirname "$file")"

    {
        printf "llm:\n"
        printf "  providers:\n"
        printf "    - name: %s\n"        "${SELECTED_PROVIDER}"
        printf "      type: %s\n"        "${SELECTED_PROVIDER}"
        printf "      model: %s\n"       "${SELECTED_MODEL}"
        printf "      api_key_env: %s\n" "${SELECTED_API_KEY_ENV}"
        [[ -n "${SELECTED_API_URL_ENV}" ]] && \
            printf "      api_url_env: %s\n" "${SELECTED_API_URL_ENV}"
        printf "\n"
        printf "ui:\n"
        printf "  theme: dark\n"
        printf "  show_timestamps: true\n"
        printf "\n"
        printf "compaction:\n"
        printf "  enabled: true\n"
        printf "  max_tokens: 150000\n"
        printf "  compact_threshold: 30000\n"
        printf "  strategy: summarize\n"
        printf "  keep_recent: 5\n"
    } > "$file"
}

maybe_backup() {
    local file="$1"
    [[ ! -f "$file" ]] && return 0
    warn "$(basename "$file") already exists."
    if ! prompt_yn "Overwrite?" "n"; then
        info "Keeping existing file."
        return 1
    fi
    local bak="${file}.bak.$(date +%Y%m%d-%H%M%S)"
    cp "$file" "$bak"
    ok "Backed up to $(basename "$bak")"
    return 0
}

# ─────────────────────────────────────────────────────────────────────────────
# Mode 1: Standalone TUI
# ─────────────────────────────────────────────────────────────────────────────

standalone_mode() {
    step "Standalone TUI"
    info "Single binary with embedded LLM — no daemon needed."

    select_provider

    local config_file="$HOME/.config/stoa/config.yaml"
    if maybe_backup "$config_file"; then
        write_llm_config "$config_file"
        ok "Wrote $config_file"
    fi

    hr
    echo
    echo "$(bold "Next steps — standalone TUI")"
    echo
    echo "  $(bold "1. Build:")"
    echo "     $(cyan "make build-chat")"
    echo
    echo "  $(bold "2. Run:")"
    if [[ "$SELECTED_PROVIDER" == "anthropic" ]]; then
        echo "     $(cyan "ANTHROPIC_API_KEY=sk-ant-... ./bin/debug/chat")"
    else
        echo "     $(cyan "${SELECTED_API_KEY_ENV}=<your-key> ./bin/debug/chat")"
        [[ -n "$SELECTED_API_URL" ]] && \
            info "  also set ${SELECTED_API_URL_ENV}=${SELECTED_API_URL}"
    fi
    echo
}

# ─────────────────────────────────────────────────────────────────────────────
# Mode 2: agentd + TUI
# ─────────────────────────────────────────────────────────────────────────────

agentd_mode() {
    step "agentd + TUI"
    info "agentd owns all LLM/tool logic; the TUI connects as a thin client."
    info "You can run agentd on a remote host and connect multiple clients."

    select_provider

    local config_file="$HOME/.config/stoa/config.yaml"
    if maybe_backup "$config_file"; then
        write_llm_config "$config_file"
        ok "Wrote $config_file"
    fi

    printf "  agentd listen port [7777]: "
    local port
    read -r port
    port="${port:-7777}"

    hr
    echo
    echo "$(bold "Next steps — agentd + TUI")"
    echo
    echo "  $(bold "1. Build:")"
    echo "     $(cyan "make build-agentd build-chat")"
    echo
    echo "  $(bold "2. Start agentd:")"
    if [[ "$SELECTED_PROVIDER" == "anthropic" ]]; then
        echo "     $(cyan "ANTHROPIC_API_KEY=sk-ant-... LLM_PROVIDER=anthropic ./bin/debug/agentd --port ${port}")"
    else
        echo "     $(cyan "${SELECTED_API_KEY_ENV}=<key> ./bin/debug/agentd --port ${port}")"
        [[ -n "$SELECTED_API_URL" ]] && \
            info "  also set ${SELECTED_API_URL_ENV}=${SELECTED_API_URL}"
    fi
    echo
    echo "  $(bold "3. Connect the TUI:")"
    echo "     $(cyan "AGENT_ADDRESS=http://localhost:${port} ./bin/debug/chat")"
    echo
    info "Tip: add 'agent.address: http://localhost:${port}' to $config_file"
    info "     to avoid setting AGENT_ADDRESS on every run."
    echo
}

# ─────────────────────────────────────────────────────────────────────────────
# Mode 3: Containerized
# ─────────────────────────────────────────────────────────────────────────────

check_container_prereqs() {
    step "Checking prerequisites"
    local all_ok=true

    if require_cmd podman; then
        ok "podman $(podman --version 2>/dev/null | awk '{print $3}')"
    else
        all_ok=false
    fi

    if require_cmd podman-compose; then
        ok "podman-compose $(podman-compose --version 2>/dev/null | head -1)"
    elif require_cmd docker-compose; then
        warn "podman-compose not found — falling back to docker-compose. Some commands may differ."
        COMPOSE_CMD="docker-compose"
    else
        fail "Neither podman-compose nor docker-compose found in PATH."
        all_ok=false
    fi

    if require_cmd openssl; then
        ok "openssl $(openssl version | awk '{print $2}')"
    else
        fail "openssl is required to generate secrets."
        all_ok=false
    fi

    $all_ok || { echo; red "Install missing prerequisites and re-run this script."; echo; exit 1; }
}

generate_env() {
    local src="$REPO_ROOT/.env.example"
    local dst="$REPO_ROOT/.env"

    [[ ! -f "$src" ]] && { fail ".env.example not found at $src"; exit 1; }

    if [[ -f "$dst" ]]; then
        if ! maybe_backup "$dst"; then
            return
        fi
    fi

    cp "$src" "$dst"

    # Entries: "VAR_NAME:bytes"
    # openssl rand -hex N produces N*2 hex characters (N bytes).
    local secrets=(
        "AGENTD_DB_PASSWORD:32"
        "AUTHD_DB_PASSWORD:32"
        "LOCAL_IDP_DB_PASSWORD:32"
        "APIKEYD_DB_PASSWORD:32"
        "AUTHD_OAUTH_LOCAL_CLIENT_SECRET:32"
        "AUTHD_ADMIN_TOKEN:32"
        "GARAGE_RPC_SECRET:32"
        "GARAGE_ADMIN_TOKEN:32"
        "GARAGE_ACCESS_KEY_ID:16"
        "GARAGE_SECRET_ACCESS_KEY:32"
        "ATTACHMENT_SECRET:32"
    )

    local var bytes val
    for entry in "${secrets[@]}"; do
        var="${entry%%:*}"
        bytes="${entry##*:}"
        val="$(openssl rand -hex "$bytes")"
        set_env_var "$dst" "$var" "$val"
        ok "Generated $var"
    done
}

prompt_external_urls() {
    step "External URLs"
    info "Press Enter to keep localhost defaults (fine for local dev)."
    echo

    local vars=(
        "WEBD_EXTERNAL_URL:Web UI:http://localhost:3000"
        "AUTHD_EXTERNAL_URL:Auth service:http://localhost:8888"
        "LOCAL_IDP_EXTERNAL_URL:Local identity provider:http://localhost:9999"
        "APIKEYD_EXTERNAL_URL:API key service:http://localhost:8889"
    )

    local var label default val
    for entry in "${vars[@]}"; do
        var="${entry%%:*}"
        rest="${entry#*:}"
        label="${rest%%:*}"
        default="${rest##*:}"
        printf "  %s [$(dim "${default}")]: " "$label"
        read -r val
        val="${val:-$default}"
        set_env_var "$REPO_ROOT/.env" "$var" "$val"
    done

    # Derived values that must stay in sync with the above.
    local authd_url webd_url apikeyd_url
    authd_url="$(grep "^AUTHD_EXTERNAL_URL=" "$REPO_ROOT/.env" | cut -d= -f2-)"
    webd_url="$(grep  "^WEBD_EXTERNAL_URL="  "$REPO_ROOT/.env" | cut -d= -f2-)"
    apikeyd_url="$(grep "^APIKEYD_EXTERNAL_URL=" "$REPO_ROOT/.env" | cut -d= -f2-)"

    set_env_var "$REPO_ROOT/.env" "LOCAL_IDP_REDIRECT_URIS" "${authd_url}/oauth/callback"
    ok "Set LOCAL_IDP_REDIRECT_URIS=${authd_url}/oauth/callback"

    set_env_var "$REPO_ROOT/.env" "AUTHD_ALLOWED_REDIRECT_ORIGINS" "${webd_url},${apikeyd_url}"
    ok "Set AUTHD_ALLOWED_REDIRECT_ORIGINS=${webd_url},${apikeyd_url}"
}

prompt_llm_gateway() {
    step "LLM gateway endpoint"
    info "Sets GATEWAY_API_URL in .env — the upstream your inference-gateway forwards to."
    info "The API key itself is stored as a Podman secret (configured next)."
    echo
    echo "  1) Anthropic  $(dim "→ https://api.anthropic.com")"
    echo "  2) OpenAI     $(dim "→ https://api.openai.com/v1")"
    echo "  3) Other      $(dim "(enter URL manually)")"
    echo
    printf "  Choice [1]: "
    local choice gateway_url
    read -r choice
    choice="${choice:-1}"

    case "$choice" in
        1) gateway_url="https://api.anthropic.com" ;;
        2) gateway_url="https://api.openai.com/v1" ;;
        3) printf "  API base URL: "; read -r gateway_url ;;
        *) fail "Invalid choice."; exit 1 ;;
    esac

    set_env_var "$REPO_ROOT/.env" "GATEWAY_API_URL" "$gateway_url"
    ok "Set GATEWAY_API_URL=$gateway_url"
}

setup_llm_secret() {
    step "LLM API key"
    info "API keys are stored as Podman secrets and never written to .env."
    echo

    if ! prompt_yn "Set up LLM API key now?" "y"; then
        info "Skipped. Run ./scripts/setup-secrets.sh before starting services."
        return
    fi

    local secrets_script="$SCRIPT_DIR/setup-secrets.sh"
    if [[ -x "$secrets_script" ]]; then
        bash "$secrets_script"
    else
        warn "setup-secrets.sh not found at $secrets_script."
        info "Set the key manually:"
        echo "     $(cyan "echo 'sk-ant-...' | podman secret create anthropic_api_key -")"
    fi
}

print_container_next_steps() {
    local webd_url
    webd_url="$(grep "^WEBD_EXTERNAL_URL=" "$REPO_ROOT/.env" 2>/dev/null | cut -d= -f2-)" \
        || webd_url="http://localhost:3000"

    hr
    echo
    echo "$(bold "Next steps — containerized stack")"
    echo
    echo "  $(bold "1. Build all container images:")"
    echo "     $(cyan "make podman-build-all")"
    echo
    echo "  $(bold "2. Run database migrations (in order):")"
    echo "     $(cyan "${COMPOSE_CMD} --profile migrate run --rm migrate-authd")"
    echo "     $(cyan "${COMPOSE_CMD} --profile migrate run --rm migrate-agentd")"
    echo "     $(cyan "${COMPOSE_CMD} --profile migrate run --rm migrate-localidp")"
    echo "     $(cyan "${COMPOSE_CMD} --profile migrate run --rm migrate-apikeyd")"
    echo
    echo "  $(bold "3. Create your first user account:")"
    echo "     $(cyan "${COMPOSE_CMD} --profile migrate run --rm migrate-localidp \\")"
    echo "       $(cyan "idp user create --username <you> --email <you@example.com> --password <pass>")"
    echo
    echo "  $(bold "4. Start all services:")"
    echo "     $(cyan "${COMPOSE_CMD} up -d")"
    echo
    echo "  $(bold "5. Open the web UI:")"
    echo "     $(cyan "$webd_url")"
    echo
    echo "  $(bold "6. (Optional) Wire up the Zulip bridge service token:")"
    echo "     $(cyan "${COMPOSE_CMD} --profile migrate run --rm migrate-authd \\")"
    echo "       $(cyan "authd service create zulip-bridge")"
    info "  Paste the printed token into ZULIP_BRIDGE_SERVICE_TOKEN in .env,"
    info "  then run: ${COMPOSE_CMD} up -d zulip-bridge"
    echo
    info "See docs/CONTAINERIZATION.md and docs/PODMAN.md for full reference."
    echo
}

container_mode() {
    check_container_prereqs

    step "Generating .env"
    generate_env

    prompt_external_urls
    prompt_llm_gateway
    setup_llm_secret
    print_container_next_steps
}

# ─────────────────────────────────────────────────────────────────────────────
# Main
# ─────────────────────────────────────────────────────────────────────────────

main() {
    echo
    echo "$(bold "╔══════════════════════════════════════╗")"
    echo "$(bold "║        stoa — setup wizard           ║")"
    echo "$(bold "╚══════════════════════════════════════╝")"
    echo
    echo "  Which deployment mode are you setting up?"
    echo
    echo "  $(bold "1)") Standalone TUI"
    info "     Single binary with embedded LLM — simplest."
    echo
    echo "  $(bold "2)") agentd + TUI  $(dim "(recommended)")"
    info "     Daemon owns LLM/tool logic; TUI is a thin client."
    echo
    echo "  $(bold "3)") Containerized  $(dim "(Podman)")"
    info "     Full stack: agentd, auth, web UI, MCP servers in containers."
    echo
    printf "  Choice [2]: "
    local choice
    read -r choice
    choice="${choice:-2}"

    case "$choice" in
        1) standalone_mode ;;
        2) agentd_mode ;;
        3) container_mode ;;
        *)
            fail "Invalid choice."
            exit 1
            ;;
    esac
}

main "$@"
