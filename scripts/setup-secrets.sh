#!/usr/bin/env bash
# setup-secrets.sh — interactively configure podman secrets for stoa
#
# Prompts for LLM API keys and stores them as podman secrets so they never
# need to appear in .env or compose files.
#
# Usage:
#   ./scripts/setup-secrets.sh                        # interactive menu
#   ./scripts/setup-secrets.sh --list                 # show current secret status
#   ./scripts/setup-secrets.sh --delete               # remove all managed secrets
#   ./scripts/setup-secrets.sh --set NAME=VALUE       # set a secret non-interactively
#   ./scripts/setup-secrets.sh --import [.env file]   # import keys from a .env file

set -euo pipefail

# -----------------------------------------------------------------------------
# Secret definitions
# Format: "name:label:key_prefix:containers"
#   key_prefix  — expected value prefix for format validation (empty = no check)
#   containers  — space-separated list of containers that use this secret
# -----------------------------------------------------------------------------

SECRETS=(
    "anthropic_api_key:Anthropic API key:sk-ant-:agentd llm-proxy"
    "openai_api_key:OpenAI API key:sk-:agentd llm-proxy"
    "gemini_api_key:Gemini API key::llm-proxy"
)

# Containers to recreate after any secret changes (deduped at runtime)
CHANGED_CONTAINERS=()

# -----------------------------------------------------------------------------
# Colour helpers
# -----------------------------------------------------------------------------

bold()  { printf '\033[1m%s\033[0m' "$*"; }
green() { printf '\033[32m%s\033[0m' "$*"; }
yellow(){ printf '\033[33m%s\033[0m' "$*"; }
red()   { printf '\033[31m%s\033[0m' "$*"; }
cyan()  { printf '\033[36m%s\033[0m' "$*"; }

# -----------------------------------------------------------------------------
# Secret field accessors (avoid local-in-case scoping issues by keeping
# all parsing in dedicated functions)
# -----------------------------------------------------------------------------

secret_name()       { echo "${1%%:*}"; }
secret_label()      { local r="${1#*:}"; echo "${r%%:*}"; }
secret_prefix()     { local r="${1#*:}"; r="${r#*:}"; echo "${r%%:*}"; }
secret_containers() { local r="${1#*:}"; r="${r#*:}"; echo "${r#*:}"; }

# -----------------------------------------------------------------------------
# Podman helpers
# -----------------------------------------------------------------------------

secret_exists() {
    podman secret inspect "$1" &>/dev/null
}

create_or_update_secret() {
    local name="$1"
    local value="$2"
    if secret_exists "$name"; then
        podman secret rm "$name" >/dev/null
    fi
    printf '%s' "$value" | podman secret create "$name" - >/dev/null
}

# Record which containers were affected so we can suggest a recreate at the end
mark_containers_changed() {
    local containers="$1"
    local c
    for c in $containers; do
        # Only add if not already present
        local already=false
        local existing
        for existing in "${CHANGED_CONTAINERS[@]+"${CHANGED_CONTAINERS[@]}"}"; do
            [[ "$existing" == "$c" ]] && already=true && break
        done
        $already || CHANGED_CONTAINERS+=("$c")
    done
}

# -----------------------------------------------------------------------------
# Validation
# -----------------------------------------------------------------------------

validate_key_format() {
    local value="$1"
    local prefix="$2"
    local label="$3"
    [[ -z "$prefix" ]] && return 0
    if [[ "$value" != "$prefix"* ]]; then
        echo "  $(yellow "Warning: $label typically starts with '${prefix}...'. Double-check your key.")"
    fi
}

# -----------------------------------------------------------------------------
# Core prompt
# -----------------------------------------------------------------------------

# prompt_secret NAME LABEL PREFIX CONTAINERS
prompt_secret() {
    local name="$1"
    local label="$2"
    local prefix="$3"
    local containers="$4"
    local current_status value replace

    if secret_exists "$name"; then
        current_status="$(yellow "already set")"
    else
        current_status="$(red "not set")"
    fi

    echo
    echo "  $(bold "$label")"
    printf "  Used by: $(cyan "%s")\n" "$containers"
    echo "  Secret:  $name  [${current_status}]"

    if secret_exists "$name"; then
        printf "  Replace existing value? [y/N] "
        read -r replace
        [[ "$replace" =~ ^[Yy]$ ]] || return 0
    fi

    printf "  Enter value (input hidden): "
    read -rs value
    echo

    if [[ -z "$value" ]]; then
        echo "  $(yellow "Skipped (empty input).")"
        return 0
    fi

    validate_key_format "$value" "$prefix" "$label"
    create_or_update_secret "$name" "$value"
    mark_containers_changed "$containers"
    echo "  $(green "Saved.")"
}

# -----------------------------------------------------------------------------
# Bulk operations
# -----------------------------------------------------------------------------

list_secrets() {
    local name label containers entry
    echo
    echo "$(bold "Managed secret status:")"
    echo
    printf "  %-25s %-8s %s\n" "Secret" "Status" "Used by"
    printf "  %-25s %-8s %s\n" "------" "------" "-------"
    for entry in "${SECRETS[@]}"; do
        name="$(secret_name "$entry")"
        label="$(secret_label "$entry")"
        containers="$(secret_containers "$entry")"
        if secret_exists "$name"; then
            printf "  %-25s %-18s %s\n" "$name" "$(green "set")" "$containers"
        else
            printf "  %-25s %-18s %s\n" "$name" "$(red "not set")" "$containers"
        fi
    done
    echo
}

delete_secrets() {
    local name entry
    echo
    echo "$(bold "Removing all managed secrets...")"
    for entry in "${SECRETS[@]}"; do
        name="$(secret_name "$entry")"
        if secret_exists "$name"; then
            podman secret rm "$name" >/dev/null
            echo "  Removed: $name"
        else
            echo "  Skipped (not found): $name"
        fi
    done
    echo
}

import_from_env() {
    local env_file="${1:-.env}"
    if [[ ! -f "$env_file" ]]; then
        echo "  $(red "File not found: $env_file")"
        return 1
    fi

    echo
    echo "$(bold "Importing from $env_file...")"

    # Map env var names to secret names
    declare -A env_to_secret=(
        [ANTHROPIC_API_KEY]=anthropic_api_key
        [OPENAI_API_KEY]=openai_api_key
        [GEMINI_API_KEY]=gemini_api_key
    )

    local found=false
    local var value secret_n entry prefix label containers
    for var in "${!env_to_secret[@]}"; do
        # Parse value from env file (skip comments, handle KEY=VALUE or KEY="VALUE")
        value="$(grep -E "^${var}=" "$env_file" 2>/dev/null | head -1 | cut -d= -f2- | sed "s/^['\"]//;s/['\"]$//" || true)"
        [[ -z "$value" ]] && continue

        secret_n="${env_to_secret[$var]}"

        # Look up metadata for this secret
        prefix=""
        label="$secret_n"
        containers=""
        for entry in "${SECRETS[@]}"; do
            if [[ "$(secret_name "$entry")" == "$secret_n" ]]; then
                prefix="$(secret_prefix "$entry")"
                label="$(secret_label "$entry")"
                containers="$(secret_containers "$entry")"
                break
            fi
        done

        echo
        echo "  $(bold "$label") (from $var)"
        printf "  Used by: $(cyan "%s")\n" "$containers"

        if secret_exists "$secret_n"; then
            printf "  Secret already set — replace? [y/N] "
            local replace
            read -r replace
            [[ "$replace" =~ ^[Yy]$ ]] || continue
        fi

        validate_key_format "$value" "$prefix" "$label"
        create_or_update_secret "$secret_n" "$value"
        mark_containers_changed "$containers"
        echo "  $(green "Saved.")"
        found=true
    done

    if ! $found; then
        echo "  $(yellow "No recognised API key variables found in $env_file.")"
    fi
}

# -----------------------------------------------------------------------------
# Post-change advice
# -----------------------------------------------------------------------------

print_recreate_hint() {
    [[ ${#CHANGED_CONTAINERS[@]} -eq 0 ]] && return
    echo
    echo "$(bold "Secrets updated. Recreate affected containers to pick up the changes:")"
    echo
    echo "  podman compose up -d --force-recreate ${CHANGED_CONTAINERS[*]}"
    echo
}

# -----------------------------------------------------------------------------
# Main menu
# -----------------------------------------------------------------------------

main_menu() {
    while true; do
        echo
        echo "$(bold "=== stoa secret setup ===")"
        echo
        echo "  1) Configure all secrets"
        echo "  2) Configure a single secret"
        echo "  3) Import from .env file"
        echo "  4) Show secret status"
        echo "  5) Delete all secrets"
        echo "  6) Exit"
        echo
        printf "Choice: "
        local choice
        read -r choice

        case "$choice" in
            1)
                echo
                echo "$(bold "Configuring all secrets")"
                echo "Press Enter to skip any key you don't use."
                local entry
                for entry in "${SECRETS[@]}"; do
                    prompt_secret \
                        "$(secret_name "$entry")" \
                        "$(secret_label "$entry")" \
                        "$(secret_prefix "$entry")" \
                        "$(secret_containers "$entry")"
                done
                echo
                echo "$(green "Done.")"
                print_recreate_hint
                ;;
            2)
                echo
                echo "$(bold "Select a secret to configure:")"
                echo
                local i=1 entry label
                for entry in "${SECRETS[@]}"; do
                    label="$(secret_label "$entry")"
                    printf "  %d) %s  $(cyan "[%s]")\n" "$i" "$label" "$(secret_containers "$entry")"
                    i=$((i + 1))
                done
                echo
                printf "Choice: "
                local idx
                read -r idx
                if [[ "$idx" =~ ^[0-9]+$ ]] && (( idx >= 1 && idx <= ${#SECRETS[@]} )); then
                    local sel="${SECRETS[$((idx-1))]}"
                    prompt_secret \
                        "$(secret_name "$sel")" \
                        "$(secret_label "$sel")" \
                        "$(secret_prefix "$sel")" \
                        "$(secret_containers "$sel")"
                    print_recreate_hint
                else
                    echo "  $(red "Invalid selection.")"
                fi
                ;;
            3)
                printf "  Path to .env file [.env]: "
                local env_path
                read -r env_path
                import_from_env "${env_path:-.env}"
                print_recreate_hint
                ;;
            4)
                list_secrets
                ;;
            5)
                printf "  $(red "This will remove all managed secrets. Are you sure?") [y/N] "
                local confirm
                read -r confirm
                [[ "$confirm" =~ ^[Yy]$ ]] && delete_secrets
                ;;
            6)
                print_recreate_hint
                echo
                exit 0
                ;;
            *)
                echo "  $(red "Invalid choice.")"
                ;;
        esac
    done
}

# -----------------------------------------------------------------------------
# Non-interactive: --set NAME=VALUE
# -----------------------------------------------------------------------------

cmd_set() {
    local arg="$1"
    local name="${arg%%=*}"
    local value="${arg#*=}"

    if [[ "$name" == "$arg" || -z "$value" ]]; then
        echo "$(red "Usage: --set NAME=VALUE")" >&2
        exit 1
    fi

    # Validate name is one we manage
    local entry found=false prefix label containers
    for entry in "${SECRETS[@]}"; do
        if [[ "$(secret_name "$entry")" == "$name" ]]; then
            found=true
            prefix="$(secret_prefix "$entry")"
            label="$(secret_label "$entry")"
            containers="$(secret_containers "$entry")"
            break
        fi
    done

    if ! $found; then
        echo "$(red "Unknown secret '$name'. Managed secrets:")" >&2
        for entry in "${SECRETS[@]}"; do echo "  $(secret_name "$entry")" >&2; done
        exit 1
    fi

    validate_key_format "$value" "$prefix" "$label"
    create_or_update_secret "$name" "$value"
    echo "$(green "Saved $name.")"
    mark_containers_changed "$containers"
    print_recreate_hint
}

# -----------------------------------------------------------------------------
# Entry point
# -----------------------------------------------------------------------------

if ! command -v podman &>/dev/null; then
    echo "$(red "Error: podman is not installed or not in PATH.")" >&2
    exit 1
fi

case "${1:-}" in
    --list)   list_secrets; exit 0 ;;
    --delete) delete_secrets; exit 0 ;;
    --import) import_from_env "${2:-.env}"; print_recreate_hint; exit 0 ;;
    --set)
        [[ -z "${2:-}" ]] && { echo "Usage: $0 --set NAME=VALUE" >&2; exit 1; }
        cmd_set "$2"
        exit 0
        ;;
    "") main_menu ;;
    *)
        echo "Usage: $0 [--list | --delete | --import [file] | --set NAME=VALUE]" >&2
        exit 1
        ;;
esac
