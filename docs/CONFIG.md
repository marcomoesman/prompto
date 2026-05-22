# prompto Configuration

prompto reads two JSON files at startup:

1. **Global** — typically `~/.config/prompto/config.json` (Linux/macOS) or
   `%AppData%\prompto\config.json` (Windows). Holds your providers, API keys,
   default model, and any cross-project preferences.
2. **Project** — `.prompto/config.json` in the current working directory.
   Optional. Layered on top of global. Restricted: project configs CANNOT
   override provider `api_key` or `base_url` (defence against cloned repos
   shipping malicious config).

Both files are JSON5-free strict JSON. Unknown fields are silently ignored,
so a typo in a key name will not produce an error — it will produce a
default-valued field.

## File locations

prompto resolves the global config path in this order; the first existing
file wins:

1. `$XDG_CONFIG_HOME/prompto/config.json`
2. `$HOME/.config/prompto/config.json`
3. The platform-default user config dir (`os.UserConfigDir()`):
   - Windows: `%AppData%\prompto\config.json`
   - macOS:   `$HOME/Library/Application Support/prompto/config.json`
   - Linux:   `$XDG_CONFIG_HOME/prompto/config.json` (same as #1)

Project config is always at `<cwd>/.prompto/config.json`.

If a global config is missing prompto reports a validation error at
startup (no providers configured). If the project config is missing,
prompto runs with the global config alone — that's the steady state for
most projects.

## Layering rules

When both files exist, prompto loads global first, then overlays project
on top. Per field:

| Field                                  | Project may override?      |
|----------------------------------------|----------------------------|
| `providers[*].kind`                    | yes                        |
| `providers[*].models`                  | yes (replaces wholesale)   |
| `providers[*].max_parallel`            | yes                        |
| `providers[*].local_provider`          | yes (only `true` wins)     |
| `providers[*].api_key`                 | **NO** (stderr warning)    |
| `providers[*].base_url`                | **NO** (stderr warning)    |
| `default.provider`, `default.model`    | yes                        |
| `rules.*`, `context.*`, `compact.*`    | yes                        |
| `agent.max_steps`                      | yes                        |
| `model_guidance.*`                     | yes                        |
| `search` (entire block)                | yes (whole-block replace)  |

When a project config tries to set `api_key` or `base_url`, prompto prints
a warning to stderr at startup and ignores that field. The rest of the
project config still applies.

## Schema

Below is every config section in order. All fields are optional unless
marked **required**.

### `providers` (required, ≥ 1 entry)

Map of provider keys to provider entries. The key is whatever name you
want to reference in `default.provider`.

```json
"providers": {
  "anthropic": {
    "kind": "anthropic",
    "api_key": "$ANTHROPIC_API_KEY",
    "models": [
      { "name": "claude-sonnet-4-6", "max_tokens": 8192 }
    ]
  }
}
```

Fields per provider:

- **`kind`** *(required)* — `"anthropic"` or `"openai"`. Selects the wire
  format. Any OpenAI-compatible local server (llama.cpp, LM Studio,
  Ollama's `/v1` shim, vLLM) uses `"openai"`.
- **`api_key`** — literal string, OR `"$ENV_VAR_NAME"` which expands at
  startup via `os.Getenv`. Required for non-local Anthropic/OpenAI; may
  be empty or a placeholder for local servers.
- **`base_url`** — full URL with scheme (e.g.
  `"https://openrouter.ai/api/v1"` or `"http://localhost:1234"`).
  Optional for the official Anthropic/OpenAI endpoints (defaults baked
  in). Validated as a well-formed `http://` or `https://` URL at startup.
- **`models`** — array of model entries the picker exposes. See
  [models](#model-entries) below.
- **`max_parallel`** *(default: 128)* — provider-wide concurrent
  request cap. Local LLM servers should set this to `1` (they usually
  serialise on a single GPU); cloud providers can leave it at the
  default. Individual models may override via `models[*].max_parallel`.
- **`local_provider`** — `true` opts the session into prompt sections
  designed to help weaker models (anti-injection warnings against
  textual tool calls, etc.). Auto-detected for `localhost` / private
  IP `base_url`s; set explicitly only when running behind a proxy or
  unusual host.

#### Model entries

Each entry is an object. The string-shorthand form (`"models": ["gpt-4o"]`)
parses for backward-compatibility but won't pass validation since
`max_tokens` is mandatory.

```json
{
  "name": "qwen-coder:30b",
  "max_tokens": 16384,
  "max_parallel": 1,
  "temperature": 0.7,
  "presence_penalty": 0.0
}
```

Fields:

- **`name`** *(required)* — the model identifier sent to the provider.
- **`max_tokens`** *(required, > 0)* — output-token cap. Reasoning
  models that emit `<think>` before answering need 8192+ to avoid
  mid-response truncation. Anthropic models usually do fine at 8192;
  local 30B+ thinking models often want 16384.
- **`max_parallel`** *(0 = inherit)* — per-model override of the
  provider's `max_parallel`. Useful when one model on a shared local
  server needs a stricter cap than its siblings.
- **`context_limit`** *(0 = inherit)* — input-context window in
  tokens. Overrides the provider's reported value for this model.
  Required in practice for OpenAI-compatible / local servers
  (`/v1/models` doesn't expose a context size, so without this
  prompto falls back to `context.default_limit` which may be wrong
  for the specific model you're running). Also useful for cloud
  variants with non-default windows (e.g. the 1M-context Anthropic
  beta). Still capped at `context.max_override`.
- **`temperature`** *(0.0–2.0)* — sampling temperature. Omit to use the
  server default (1.0 for most providers). Lower = more deterministic.
- **`presence_penalty`** *(-2.0–2.0)* — OpenAI-compatible only.
  Anthropic ignores this field. Discourages the model from repeating
  topics already in the conversation.

### `default` (required)

Names the provider + model used at startup.

```json
"default": {
  "provider": "anthropic",
  "model": "claude-sonnet-4-6"
}
```

- **`provider`** *(required)* — must be a key in `providers`.
- **`model`** *(required)* — must be listed under `providers[default.provider].models`.

The user can switch model at runtime via `/model` and the choice persists
on the session row; `default.*` only applies to fresh sessions.

### `rules`

```json
"rules": {
  "files": ["AGENTS.md", "CLAUDE.md"],
  "respect_robots_txt": false
}
```

- **`files`** *(default `["AGENTS.md"]`)* — filenames prompto loads
  hierarchically from cwd up to repo root, injecting them into the
  system prompt as project instructions. Order matters: later files
  override earlier ones in case of duplicate keys.
- **`respect_robots_txt`** *(default `false`)* — when `true`, the
  webfetch tool honours `Disallow:` directives in the target host's
  `/robots.txt`. Default is off because a coding agent fetches docs for
  users who already have permission, and a surprise block on `/` would
  be unhelpful. Opt in when running against hostile or
  rate-limit-sensitive sites.

### `context`

Per-model context-window sizing. Both fields are in tokens.

```json
"context": {
  "default_limit": 200000,
  "max_override": 400000
}
```

- **`default_limit`** *(default 200_000)* — fallback context size when
  the provider reports 0 (unknown model). Used by the compactor to
  decide when to summarize.
- **`max_override`** *(default 400_000)* — cap applied on top of any
  provider-reported context. Prevents runaway summarization cost when
  pointing prompto at a 1M-context variant — you opt into the bigger
  budget by raising this.

### `compact`

Threshold-based conversation summarization.

```json
"compact": {
  "model": "claude-haiku-4-5",
  "threshold_pct": 80,
  "keep_recent_messages": 8
}
```

- **`model`** — summarizer model. Empty inherits the session model.
  Cheaper-and-fast models (Haiku, GPT-mini, a smaller local model) are
  good choices.
- **`threshold_pct`** *(default 80)* — percentage of effective context
  at which summarization fires. At 80% of a 200k window (= 160k), the
  conversation head gets summarized into a `<compact_summary>` block.
- **`keep_recent_messages`** *(default 8)* — number of trailing
  messages preserved verbatim during summarization. One user-visible
  turn is typically 2–4 messages (assistant text + tool_use +
  tool_result), so 8 ≈ the last 2–4 round-trips. The legacy key
  `keep_recent_turns` is still accepted for backward compat.

### `agent`

```json
"agent": {
  "max_steps": 100
}
```

- **`max_steps`** *(default 100)* — per-turn cap on provider
  round-trips a primary agent may issue before terminating with
  `ErrMaxSteps`. Subagents have their own internal caps; this only
  bounds the top-level run. Raise it for long unattended runs (file
  scan + many edits + verification can blow past 50); lower it to
  bound cost on a session-by-session basis. `0` inherits the default.

### `model_guidance`

Deterministic reliability aids that help weaker models. All four
fields are 3- or 2-state strings.

```json
"model_guidance": {
  "tool_call_recovery": "auto",
  "workspace_hints": "on",
  "loop_guards": "on",
  "compact_tool_schemas": "auto"
}
```

- **`tool_call_recovery`** *(`auto`|`on`|`off`, default `auto`)* —
  detects textual `<tool_call>…</tool_call>` blocks in assistant
  output and re-routes them through the structured tool-calling API.
  `auto` enables it only for `local_provider: true`.
- **`workspace_hints`** *(`on`|`off`, default `on`)* — injects a
  one-line workspace summary into the system prompt
  (`<cwd>/<branch>/<file count>`) so the model has a stable orientation
  cue across turns.
- **`loop_guards`** *(`on`|`off`, default `on`)* — detects identical
  repeated tool calls within a turn and prompts the model to change
  approach. The primary "stuck-loop" defence; the `max_steps` ceiling
  is now a runaway-cost backstop rather than the main safety net.
- **`compact_tool_schemas`** *(`auto`|`on`|`off`, default `auto`)* —
  strips long descriptions from tool schemas in the system prompt for
  models with small context windows. `auto` enables it for
  `local_provider: true`.

### `search`

The websearch tool's backend. Omit entirely to disable websearch.

```json
"search": {
  "provider": "tavily",
  "api_key": "$TAVILY_API_KEY"
}
```

- **`provider`** *(required when `search` is set)* — one of `tavily`,
  `exa`, `firecrawl`, `searxng`.
- **`api_key`** — literal or `"$ENV_VAR"`. Required for tavily/exa/
  firecrawl. Not used by searxng.
- **`base_url`** — required for `searxng` (point at your self-hosted
  instance). Optional override for the others. Validated as a
  well-formed `http://` or `https://` URL.

When a project config sets `search`, the **entire block** replaces the
global one. Field-level merging would leak the global `api_key` into a
project that switched providers — almost always wrong.

## Environment variables

Any `api_key` (provider OR search) starting with `$` is treated as an
env-var reference. `"api_key": "$ANTHROPIC_API_KEY"` expands at startup
to `os.Getenv("ANTHROPIC_API_KEY")`. If the variable is unset, the key
becomes empty — and prompto rejects an empty key at startup for
non-local providers.

This is the recommended way to handle credentials so your config file
can be committed (in the case of project config) without leaking
secrets.

## Validation

prompto runs validation after loading and merging. Failures abort
startup with a clear error message:

- At least one provider must be defined.
- `default.provider` must exist in `providers`.
- `default.model` must be listed under
  `providers[default.provider].models`.
- Every `models[*].max_tokens` must be > 0.
- `temperature` (if set) must be in [0.0, 2.0]; `presence_penalty`
  in [-2.0, 2.0].
- Every non-empty `base_url` (provider OR search) must parse as a
  well-formed `http://` or `https://` URL with a non-empty host.
- `agent.max_steps` must be ≥ 0.
- If `search` is set, `provider` is required; for `searxng`,
  `base_url` is required; for the others, `api_key` is required.
- `model_guidance.*` values must be one of the documented states for
  that field.

Unknown keys are silently ignored — that includes typos. If a setting
"isn't taking effect," check the key spelling first.

## Common setups

### Single cloud provider (Anthropic)

```json
{
  "providers": {
    "anthropic": {
      "kind": "anthropic",
      "api_key": "$ANTHROPIC_API_KEY",
      "models": [
        { "name": "claude-sonnet-4-6",  "max_tokens": 8192 },
        { "name": "claude-opus-4-7",    "max_tokens": 8192 },
        { "name": "claude-haiku-4-5",   "max_tokens": 8192 }
      ]
    }
  },
  "default": {
    "provider": "anthropic",
    "model": "claude-sonnet-4-6"
  },
  "compact": {
    "model": "claude-haiku-4-5"
  }
}
```

The cheap Haiku model is configured for summarization, the expensive
Opus is available via `/model` for tough tasks.

### Local model via llama.cpp / LM Studio

```json
{
  "providers": {
    "llamacpp": {
      "kind": "openai",
      "base_url": "http://localhost:8080",
      "api_key": "llamacpp",
      "local_provider": true,
      "max_parallel": 1,
      "models": [
        {
          "name": "qwen-coder-30b",
          "max_tokens": 16384,
          "temperature": 0.7,
          "presence_penalty": 1.0
        }
      ]
    }
  },
  "default": {
    "provider": "llamacpp",
    "model": "qwen-coder-30b"
  },
  "context": {
    "default_limit": 131072
  }
}
```

`max_parallel: 1` because a single-GPU server can't serve concurrent
completions. `max_tokens: 16384` gives a thinking model headroom.
`local_provider: true` opts into the weaker-model prompt sections (the
auto-detection of `localhost` would catch this too).

### Cloud + local with cloud for summarization

```json
{
  "providers": {
    "anthropic": {
      "kind": "anthropic",
      "api_key": "$ANTHROPIC_API_KEY",
      "models": [
        { "name": "claude-haiku-4-5", "max_tokens": 8192 }
      ]
    },
    "lmstudio": {
      "kind": "openai",
      "base_url": "http://localhost:1234",
      "api_key": "lm-studio",
      "local_provider": true,
      "max_parallel": 1,
      "models": [
        { "name": "qwen-coder-30b", "max_tokens": 16384 }
      ]
    }
  },
  "default": {
    "provider": "lmstudio",
    "model": "qwen-coder-30b"
  },
  "compact": {
    "model": "claude-haiku-4-5",
    "threshold_pct": 70
  }
}
```

Coding happens locally; summarization (which a small model would
butcher) goes to Haiku at 70% to keep the local model from
spending its context on history.

### Project config pinning a model

`.prompto/config.json` in a project directory:

```json
{
  "default": {
    "model": "claude-opus-4-7"
  },
  "agent": {
    "max_steps": 200
  }
}
```

This project always opens with Opus and tolerates longer autonomous
runs. The user's global provider, key, and base URL are unchanged —
remember, the project layer can't touch credentials.

## Full reference example

A maximally-explicit config touching every supported field, for
copy-and-prune:

```json
{
  "providers": {
    "anthropic": {
      "kind": "anthropic",
      "api_key": "$ANTHROPIC_API_KEY",
      "max_parallel": 8,
      "models": [
        {
          "name": "claude-sonnet-4-6",
          "max_tokens": 8192,
          "temperature": 1.0
        },
        {
          "name": "claude-haiku-4-5",
          "max_tokens": 8192
        }
      ]
    },
    "lmstudio": {
      "kind": "openai",
      "base_url": "http://localhost:1234",
      "api_key": "lm-studio",
      "local_provider": true,
      "max_parallel": 1,
      "models": [
        {
          "name": "qwen-coder-30b",
          "max_tokens": 16384,
          "context_limit": 131072,
          "temperature": 0.7,
          "presence_penalty": 1.0,
          "max_parallel": 1
        }
      ]
    },
    "openrouter": {
      "kind": "openai",
      "base_url": "https://openrouter.ai/api/v1",
      "api_key": "$OPENROUTER_API_KEY",
      "models": [
        { "name": "moonshotai/kimi-k2.6",     "max_tokens": 8192 },
        { "name": "z-ai/glm-5.1",             "max_tokens": 8192 },
        { "name": "deepseek/deepseek-v4-flash","max_tokens": 8192 }
      ]
    }
  },

  "default": {
    "provider": "anthropic",
    "model": "claude-sonnet-4-6"
  },

  "rules": {
    "files": ["AGENTS.md", "CLAUDE.md"],
    "respect_robots_txt": false
  },

  "context": {
    "default_limit": 200000,
    "max_override": 400000
  },

  "compact": {
    "model": "claude-haiku-4-5",
    "threshold_pct": 80,
    "keep_recent_messages": 8
  },

  "agent": {
    "max_steps": 100
  },

  "model_guidance": {
    "tool_call_recovery": "auto",
    "workspace_hints": "on",
    "loop_guards": "on",
    "compact_tool_schemas": "auto"
  },

  "search": {
    "provider": "tavily",
    "api_key": "$TAVILY_API_KEY"
  }
}
```

## Troubleshooting

- **"provider %q not found"** — your `default.provider` is set to a key
  that doesn't appear in `providers`.
- **"default model %q not found under providers[%q].models"** — the
  default model isn't listed under its provider's `models` array. Add
  it as an object with at least a `max_tokens`.
- **"missing API key for provider %q"** — env-var expansion produced
  an empty string. Set the variable or hardcode the key.
- **"warning: ignoring project config attempt to override
  providers[%q].api_key"** — your project config is trying to set
  credentials; that's blocked. Move credentials to the global config.
- **"local model %q is not ready: … context deadline exceeded"** — the
  startup health probe of `/v1/models` timed out (3s). Either the
  server is genuinely down, or it's loading a model and will be ready
  shortly. The warning is non-fatal; the next actual call will still
  succeed when the server is ready.
- **A setting "doesn't work"** — typo first. Unknown keys are silently
  ignored.
