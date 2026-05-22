# Prompto
Lightweight coding agent that is capable but optimized for smaller local models.

## Tech Stack

- Go 1.26, **no CGO**

## Tests & Run

Tests: `go test ./...`
Lint: `golangci-lint run`

You should always write comprehensive tests for complex components, taking care to write tests for Phase requirements.

## Project Structure

```
cmd/prompto/                     # Entry point + transcript summarizer
internal/
  api/                           # Core domain types (zero internal deps)
                                 # Message, ContentBlock, Role, ToolCall/Result, Usage,
                                 # StreamEvent, Provider interface, ToolDefinition
  sse/                           # Shared SSE parser (Parse → iter.Seq[Event])
  provider/                      # LLM provider implementations
    registry.go                  # New(ProviderConfig) factory
    anthropic/                   # Anthropic Messages API (anthropic, codec, stream)
    openai/                      # OpenAI Chat Completions API (openai, codec, stream)
  tool/                          # Tool system + built-in tools
    tool.go registry.go schema.go security.go display.go summarize.go
    bash.go read.go write.go edit.go glob.go grep.go
    todowrite.go task.go gitignore.go htmlconv.go
    webfetch.go webfetch_{chrome,idle,jar,robots,ssrf,stealth}.go (+ stealth.js)
  agent/                         # Core agentic loop
    run.go agent.go conversation.go prompt.go prompt_sections.go
    factory.go registry.go definition.go disallowed.go
    tool.go permission.go plan_mode.go compact.go
    notifier.go notifier_checkers.go agents_md.go todo.go
    filestate.go filechanges.go outputcap.go tmpspill.go
    concurrency.go store.go requestlog.go errors.go
  command/                       # Slash-command dispatch (/help, /model, /agent,
                                 # /compact, /clear, /init, /plan, /mode, /new,
                                 # /resume, /sessions, /undo, /todo, /env, /review,
                                 # /context, /quit + custom-loaded commands)
  compact/                       # Conversation compaction & summarization
                                 # compact.go summarize.go clear.go tokens.go
  config/                        # Configuration (global + project merge)
                                 # config.go defaults.go models.go
  permission/                    # Permission evaluator
                                 # evaluator.go ruleset.go mode.go plan.go
                                 # protected.go traversal.go glob.go store.go
  store/                         # SQLite-backed session/message/todo store
                                 # store.go sessions.go messages.go todos.go
                                 # filechanges.go migrations.go errors.go
                                 # schema/*.sql (numbered migrations)
  tui/                           # Bubbletea TUI
                                 # app.go chat.go input.go status.go style.go
                                 # commands.go indicators.go thinking.go welcome.go
                                 # env.go help_overlay.go model_picker.go
  httpattr/                      # User-Agent / attribution headers
  version/                       # Build version constants
```

## Key Rules and Guidance

### General
- Ask clarifying questions for ambiguous requirements.
- No guessing. Make changes based on known requirements and ground truth.
- Don't assume. Don't hide confusion. Surface tradeoffs.
- Before implementing, state your assumptions explicitly. If uncertain, ask. If multiple interpretations exist, present them - don't pick silently.
- Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

### Code Style
- Minimum code that solves the problem. Nothing speculative.
- No abstractions for single-use code.
- No error handling for impossible scenarios.
- Enforce `gofmt`, `go vet`.
- Small interfaces near consumers; prefer composition over inheritance.
- Avoid reflection on hot paths; prefer generics when it clarifies and speeds.
- Use input structs for function receiving more than 2 arguments. Input contexts should not get in the input struct.
- Declare function input structs before the function consuming them.

### Concurrency
- The **sender** closes channels; receivers never close.
- Tie goroutine lifetime to a `context.Context`; prevent leaks.
- Protect shared state with `sync.Mutex`/`atomic`; no "probably safe" races.
- Use `errgroup` for fan‑out work; cancel on first error.

### Errors
- Wrap with `%w` and context: `fmt.Errorf("open %s: %w", p, err)`.
- Use `errors.Is`/`errors.As` for control flow; no string matching.
- Define sentinel errors in the package; document behavior.

