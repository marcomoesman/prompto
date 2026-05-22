#!/usr/bin/env bash
# prompto installer for macOS and Linux.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/marcomoesman/prompto/main/install.sh | bash
#
# Flags (when invoked as a downloaded file: bash install.sh [flags]):
#   --version vX.Y.Z   install a specific version (default: latest)
#   --prefix DIR       override install dir (default: ~/.local/bin)
#   --no-config        skip the interactive config wizard
#   --no-path          skip PATH modification
#   --uninstall        remove prompto, PATH entry, and optionally config
#   -y, --yes          assume "yes" to interactive prompts (non-interactive install)
#
# Re-running the installer upgrades in place. If the latest (or pinned)
# version already matches what's installed, the binary is left alone;
# only PATH wiring and config are re-checked.

set -euo pipefail

REPO="marcomoesman/prompto"
INSTALL_TAG="# added by prompto-installer"
DEFAULT_PREFIX="${XDG_BIN_HOME:-$HOME/.local/bin}"

PREFIX="$DEFAULT_PREFIX"
VERSION=""
NO_CONFIG=0
NO_PATH=0
UNINSTALL=0
ASSUME_YES=0
SELF_TEST=0

while [ $# -gt 0 ]; do
    case "$1" in
        --version) VERSION="$2"; shift 2 ;;
        --prefix)  PREFIX="$2";  shift 2 ;;
        --no-config) NO_CONFIG=1; shift ;;
        --no-path)   NO_PATH=1;   shift ;;
        --uninstall) UNINSTALL=1; shift ;;
        --self-test) SELF_TEST=1; shift ;;
        -y|--yes)    ASSUME_YES=1; shift ;;
        -h|--help)
            sed -n '2,18p' "$0" 2>/dev/null || true
            exit 0
            ;;
        *) echo "unknown flag: $1" >&2; exit 2 ;;
    esac
done

err()  { printf 'error: %s\n' "$*" >&2; exit 1; }
info() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"
}

# ---------- platform detection ----------

detect_os() {
    case "$(uname -s)" in
        Linux)  echo linux ;;
        Darwin) echo darwin ;;
        *) err "unsupported OS: $(uname -s) (prompto supports linux and darwin via this installer)" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo amd64 ;;
        aarch64|arm64) echo arm64 ;;
        *) err "unsupported architecture: $(uname -m)" ;;
    esac
}

# ---------- shell rc helpers ----------

detect_rc_file() {
    local shell_name
    shell_name="$(basename "${SHELL:-/bin/bash}")"
    case "$shell_name" in
        zsh)  printf '%s\n' "${ZDOTDIR:-$HOME}/.zshrc" ;;
        bash)
            if [ -f "$HOME/.bashrc" ]; then printf '%s\n' "$HOME/.bashrc"
            elif [ -f "$HOME/.bash_profile" ]; then printf '%s\n' "$HOME/.bash_profile"
            else printf '%s\n' "$HOME/.bashrc"
            fi ;;
        fish) printf '%s\n' "$HOME/.config/fish/config.fish" ;;
        *)    printf '%s\n' "$HOME/.profile" ;;
    esac
}

path_already_has() {
    case ":$PATH:" in
        *":$1:"*) return 0 ;;
        *)        return 1 ;;
    esac
}

add_to_path() {
    local dir="$1"
    if path_already_has "$dir"; then
        info "$dir is already on PATH."
        return
    fi
    local rc; rc="$(detect_rc_file)"
    mkdir -p "$(dirname "$rc")"
    touch "$rc"
    if grep -Fq "$dir" "$rc" 2>/dev/null; then
        info "$rc already references $dir; not re-adding."
        return
    fi
    local line
    if [[ "$rc" == *config/fish/config.fish ]]; then
        line="set -gx PATH $dir \$PATH $INSTALL_TAG"
    else
        line="export PATH=\"$dir:\$PATH\" $INSTALL_TAG"
    fi
    printf '\n%s\n' "$line" >> "$rc"
    info "Added $dir to PATH in $rc."
    info "Open a new shell or run: source $rc"
}

remove_from_path() {
    local rc; rc="$(detect_rc_file)"
    [ -f "$rc" ] || return 0
    if ! grep -Fq "$INSTALL_TAG" "$rc"; then return 0; fi
    local tmp; tmp="$(mktemp)"
    grep -Fv "$INSTALL_TAG" "$rc" > "$tmp"
    mv "$tmp" "$rc"
    info "Removed prompto PATH entry from $rc."
}

# ---------- version resolution ----------

resolve_latest_tag() {
    require_cmd curl
    local url="https://api.github.com/repos/$REPO/releases/latest"
    local tag
    tag="$(curl -fsSL "$url" | grep '"tag_name"' | head -n1 | sed -E 's/.*"tag_name": "([^"]+)".*/\1/')"
    [ -n "$tag" ] || err "could not resolve latest release tag from $url"
    echo "$tag"
}

installed_version() {
    local bin="$1"
    [ -x "$bin" ] || { echo ""; return; }
    local out
    out="$("$bin" --version 2>/dev/null || true)"
    # Expected format: "prompto v0.1.0"
    echo "$out" | awk '{print $2}' | sed 's/^v//'
}

json_escape() {
    local s="$1"
    local out=""
    local c
    local i
    for ((i = 0; i < ${#s}; i++)); do
        c="${s:i:1}"
        case "$c" in
            '"') out="${out}\\\"" ;;
            '\') out="${out}\\\\" ;;
            $'\n') out="${out}\\n" ;;
            $'\r') out="${out}\\r" ;;
            $'\t') out="${out}\\t" ;;
            *) out="${out}${c}" ;;
        esac
    done
    printf '%s' "$out"
}

run_self_test() {
    local got
    got="$(json_escape $'a"b\\c\nd')"
    [ "$got" = 'a\"b\\c\nd' ] || err "json_escape self-test failed: $got"

    local tmp bin
    tmp="$(mktemp -d)"
    bin="$tmp/prompto"
    cat > "$bin" <<'EOF'
#!/usr/bin/env bash
printf 'prompto v1.2.3\n'
EOF
    chmod 755 "$bin"
    got="$(installed_version "$bin")"
    rm -rf "$tmp"
    [ "$got" = "1.2.3" ] || err "installed_version self-test failed: $got"

    info "installer self-test OK"
}

if [ $SELF_TEST -eq 1 ]; then
    run_self_test
    exit 0
fi

# ---------- uninstall ----------

do_uninstall() {
    local bin="$PREFIX/prompto"
    if [ -e "$bin" ]; then
        rm -f "$bin"
        info "Removed $bin."
    else
        info "No prompto binary at $bin."
    fi
    [ $NO_PATH -eq 1 ] || remove_from_path

    local cfg_dir="${XDG_CONFIG_HOME:-$HOME/.config}/prompto"
    if [ -d "$cfg_dir" ]; then
        local reply="n"
        if [ $ASSUME_YES -eq 1 ]; then
            reply="n"  # never delete config on -y unless explicitly requested
        else
            printf 'Also remove %s (config)? [y/N] ' "$cfg_dir"
            read -r reply </dev/tty || reply="n"
        fi
        case "$reply" in
            y|Y|yes|YES) rm -rf "$cfg_dir"; info "Removed $cfg_dir." ;;
            *) info "Kept $cfg_dir." ;;
        esac
    fi
    info "Uninstall complete."
}

if [ $UNINSTALL -eq 1 ]; then
    do_uninstall
    exit 0
fi

# ---------- install / upgrade ----------

require_cmd curl
require_cmd tar
require_cmd uname

# shasum on macOS, sha256sum on Linux
SHA_CMD=""
if command -v sha256sum >/dev/null 2>&1; then SHA_CMD="sha256sum"
elif command -v shasum  >/dev/null 2>&1; then SHA_CMD="shasum -a 256"
else err "neither sha256sum nor shasum is available"
fi

OS="$(detect_os)"
ARCH="$(detect_arch)"

if [ -z "$VERSION" ]; then
    VERSION="$(resolve_latest_tag)"
fi
# Normalise: tag is "vX.Y.Z", semver is "X.Y.Z"
VERSION_SEMVER="${VERSION#v}"
TAG="v$VERSION_SEMVER"

BIN_PATH="$PREFIX/prompto"
EXISTING="$(installed_version "$BIN_PATH")"
if [ -n "$EXISTING" ] && [ "$EXISTING" = "$VERSION_SEMVER" ]; then
    info "prompto v$VERSION_SEMVER is already installed at $BIN_PATH."
    info "Re-checking PATH wiring..."
    [ $NO_PATH -eq 1 ] || add_to_path "$PREFIX"
    info "Up to date. Run 'prompto --uninstall' or re-run with --version to change."
    exit 0
fi

if [ -n "$EXISTING" ]; then
    info "Upgrading prompto v$EXISTING -> v$VERSION_SEMVER..."
else
    info "Installing prompto v$VERSION_SEMVER..."
fi

ARCHIVE_NAME="prompto_${VERSION_SEMVER}_${OS}_${ARCH}.tar.gz"
ARCHIVE_URL="https://github.com/$REPO/releases/download/$TAG/$ARCHIVE_NAME"
SUMS_URL="https://github.com/$REPO/releases/download/$TAG/checksums.txt"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

info "Downloading $ARCHIVE_NAME..."
curl -fsSL -o "$TMP/$ARCHIVE_NAME" "$ARCHIVE_URL" \
    || err "failed to download $ARCHIVE_URL"

info "Downloading checksums.txt..."
curl -fsSL -o "$TMP/checksums.txt" "$SUMS_URL" \
    || err "failed to download $SUMS_URL"

EXPECTED="$(grep "  $ARCHIVE_NAME\$" "$TMP/checksums.txt" | awk '{print $1}')"
[ -n "$EXPECTED" ] || err "no entry for $ARCHIVE_NAME in checksums.txt"

ACTUAL="$(cd "$TMP" && $SHA_CMD "$ARCHIVE_NAME" | awk '{print $1}')"
if [ "$EXPECTED" != "$ACTUAL" ]; then
    err "checksum mismatch for $ARCHIVE_NAME (expected $EXPECTED, got $ACTUAL)"
fi
info "Checksum OK ($ACTUAL)."

tar -xzf "$TMP/$ARCHIVE_NAME" -C "$TMP" prompto

mkdir -p "$PREFIX"
install -m 0755 "$TMP/prompto" "$BIN_PATH"
info "Installed $BIN_PATH."

[ $NO_PATH -eq 1 ] || add_to_path "$PREFIX"

# ---------- config wizard ----------

CFG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/prompto"
CFG_FILE="$CFG_DIR/config.json"

write_config() {
    mkdir -p "$CFG_DIR"
    chmod 700 "$CFG_DIR" 2>/dev/null || true
    local tmp="$CFG_FILE.tmp.$$"
    (umask 077 && printf '%s\n' "$1" > "$tmp")
    mv "$tmp" "$CFG_FILE"
    chmod 600 "$CFG_FILE" 2>/dev/null || true
    info "Wrote $CFG_FILE."
}

run_config_wizard() {
    if [ -f "$CFG_FILE" ]; then
        info "Existing config found at $CFG_FILE; leaving it untouched."
        return
    fi
    if [ $ASSUME_YES -eq 1 ] || [ ! -t 0 ] && [ ! -r /dev/tty ]; then
        info "Skipping interactive config wizard (non-interactive shell)."
        info "Edit $CFG_FILE manually. See: https://github.com/$REPO/blob/main/docs/CONFIG.md"
        return
    fi

    exec </dev/tty
    printf '\n'
    info "Let's set up prompto's model provider."
    printf '  1) Cloud   (Anthropic, OpenAI, OpenRouter)\n'
    printf '  2) Local   (LM Studio, llama.cpp, Ollama)\n'
    printf '  3) Skip    (configure manually later)\n'
    printf 'Choice [1-3, default 3]: '
    local choice; read -r choice || choice=""
    case "${choice:-3}" in
        1) configure_cloud ;;
        2) configure_local ;;
        *) info "Skipped. Edit $CFG_FILE; see docs/CONFIG.md." ;;
    esac
}

configure_cloud() {
    printf '\nProvider:\n  1) Anthropic\n  2) OpenAI\n  3) OpenRouter\nChoice [1-3]: '
    local p; read -r p || p=""
    local kind name api_env model
    case "$p" in
        1) kind=anthropic; name=anthropic; api_env=ANTHROPIC_API_KEY; model="claude-sonnet-4-6" ;;
        2) kind=openai;    name=openai;    api_env=OPENAI_API_KEY;    model="gpt-4o" ;;
        3) kind=openai;    name=openrouter; api_env=OPENROUTER_API_KEY; model="anthropic/claude-sonnet-4-6" ;;
        *) info "Unrecognised choice; skipping."; return ;;
    esac

    local base_url_line=""
    if [ "$name" = "openrouter" ]; then
        base_url_line='      "base_url": "https://openrouter.ai/api/v1",\n'
    fi

    printf 'API key for %s (leave blank to use $%s at runtime): ' "$name" "$api_env"
    stty -echo 2>/dev/null || true
    local key; read -r key || key=""
    stty echo 2>/dev/null || true
    printf '\n'

    local api_key_value
    if [ -z "$key" ]; then
        api_key_value="\$$api_env"
    else
        api_key_value="$key"
    fi

    local api_key_json model_json
    api_key_json="$(json_escape "$api_key_value")"
    model_json="$(json_escape "$model")"

    write_config "$(printf '{\n  "providers": {\n    "%s": {\n      "kind": "%s",\n%b      "api_key": "%s",\n      "models": [\n        { "name": "%s", "max_tokens": 8192 }\n      ]\n    }\n  },\n  "default": {\n    "provider": "%s",\n    "model": "%s"\n  }\n}' \
        "$name" "$kind" "$base_url_line" "$api_key_json" "$model_json" "$name" "$model_json")"
    if [ -z "$key" ]; then
        info "Remember to export $api_env before running prompto."
    fi
}

configure_local() {
    printf '\nLocal server:\n  1) LM Studio   (http://localhost:1234)\n  2) llama.cpp   (http://localhost:8080)\n  3) Ollama      (http://localhost:11434)\nChoice [1-3]: '
    local s; read -r s || s=""
    local url placeholder default_model
    case "$s" in
        1) url="http://localhost:1234"; placeholder="lm-studio";   default_model="qwen3-coder-30b" ;;
        2) url="http://localhost:8080"; placeholder="llamacpp";    default_model="qwen-coder-30b" ;;
        3) url="http://localhost:11434"; placeholder="ollama";     default_model="qwen2.5-coder:32b" ;;
        *) info "Unrecognised choice; skipping."; return ;;
    esac

    printf 'Model name [%s]: ' "$default_model"
    local model; read -r model || model=""
    model="${model:-$default_model}"

    local model_json
    model_json="$(json_escape "$model")"

    write_config "$(printf '{\n  "providers": {\n    "local": {\n      "kind": "openai",\n      "base_url": "%s",\n      "api_key": "%s",\n      "local_provider": true,\n      "max_parallel": 1,\n      "models": [\n        {\n          "name": "%s",\n          "max_tokens": 16384,\n          "temperature": 0.7\n        }\n      ]\n    }\n  },\n  "default": {\n    "provider": "local",\n    "model": "%s"\n  }\n}' \
        "$url" "$placeholder" "$model_json" "$model_json")"
    info "Make sure your local server is running at $url before launching prompto."
}

if [ $NO_CONFIG -eq 0 ]; then
    run_config_wizard
fi

printf '\n'
info "Done. Start a new shell, then run:  prompto"
