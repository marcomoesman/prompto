package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/marcomoesman/prompto/internal/api"
)

// BuildSystemPromptInput bundles the per-turn values needed to assemble a
// sectioned system prompt. Cwd/Platform/Model/Date are stable across a
// session and live in the cacheable prefix; ProjectInstructions, Todos,
// and the workspace/verification hints (which can shift between turns)
// live in the volatile suffix.
type BuildSystemPromptInput struct {
	Cwd                 string
	Platform            string // e.g. "darwin/arm64"
	Model               string
	Date                string // pre-formatted by the caller (e.g. "2026-04-21")
	ProjectInstructions string // AGENTS.md concatenation; empty when none found

	// Tools is the agent's effective tool allowlist (post-disallow). Empty
	// means "all available" — the prompt builder emits every sub-block.
	// Sub-blocks that mention specific tools (Web Fetching, Shell Commands)
	// are filtered when those tools aren't in the list.
	Tools []string

	// LocalProvider is true when the active provider points at a local LLM
	// (Ollama, LM Studio, llama.cpp, etc.). Opts in extra prompt sections
	// that help weaker models — primarily the anti-injection warning
	// against textual tool calls.
	LocalProvider bool

	WorkspaceSummary WorkspaceSummary
	VerificationHint VerificationHint

	// Todos is the session's current todo list. Empty slice → no section
	// rendered. Populated per-turn by the run loop after loading from the
	// TodoStore so the model sees up-to-date state across compaction
	// boundaries.
	Todos []Todo
}

// BuildSystemPrompt assembles the prompt as []api.SystemBlock with one cache
// boundary between the stable prefix (identity + safety + tool-use rules +
// tone + environment) and the volatile suffix (project instructions + todos
// + workspace hints).
//
// The stable prefix is byte-identical across calls within one session for a
// fixed (Cwd, Platform, Model, Date, Tools, LocalProvider) tuple. Switching
// agents (which changes Tools), providers (which may flip LocalProvider), or
// crossing midnight produces a different prefix — that's correct behavior
// and a one-time cache miss on transition.
//
// No cross-call memoization: stable sections are computed fresh per agent-
// run start (~once per Run). The string concatenation is cheap (microseconds)
// relative to the per-turn provider call.
func BuildSystemPrompt(in BuildSystemPromptInput) []api.SystemBlock {
	stable := stableBlocks(in)
	p := NewPrompt()
	for _, s := range volatileSections(in) {
		p.AddVolatile(s)
	}
	volatile := p.SystemBlocks()
	if len(volatile) == 0 {
		return stable
	}
	out := make([]api.SystemBlock, 0, len(stable)+len(volatile))
	out = append(out, stable...)
	out = append(out, volatile...)
	return out
}

// stableBlocks computes the cacheable prefix for the given input. Replaces
// the previous sync.OnceValue-memoized version: per-(Tools, LocalProvider)
// inputs preclude a single global cache, and the cost of recomputation per
// agent-run start is negligible.
func stableBlocks(in BuildSystemPromptInput) []api.SystemBlock {
	p := NewPrompt()
	for _, s := range stableSections(in) {
		p.AddStable(s)
	}
	return p.SystemBlocks()
}

// PlanSystemPrompt builds the plan-agent prompt: a read-only investigator
// whose only writable target is the plan file. Reuses the stable build
// prefix so the prompt cache still hits across agent switches with matching
// tool allowlists.
func PlanSystemPrompt(in BuildSystemPromptInput) []api.SystemBlock {
	stable := stableBlocks(in)
	p := NewPrompt()
	p.AddVolatile(Section{Name: "plan_mode", Content: planModeSection})
	for _, s := range volatileSections(in) {
		p.AddVolatile(s)
	}
	volatile := p.SystemBlocks()
	out := make([]api.SystemBlock, 0, len(stable)+len(volatile))
	out = append(out, stable...)
	out = append(out, volatile...)
	return out
}

// ExploreSystemPrompt builds the explore-subagent prompt: a fast read-only
// investigator that returns a tight summary to the parent.
func ExploreSystemPrompt(in BuildSystemPromptInput) []api.SystemBlock {
	stable := stableBlocks(in)
	p := NewPrompt()
	p.AddVolatile(Section{Name: "explore_mode", Content: exploreModeSection})
	for _, s := range volatileSections(in) {
		p.AddVolatile(s)
	}
	volatile := p.SystemBlocks()
	out := make([]api.SystemBlock, 0, len(stable)+len(volatile))
	out = append(out, stable...)
	out = append(out, volatile...)
	return out
}

// ResearchSystemPrompt builds the research-subagent prompt: a read-only
// online investigator that returns a tight cited summary to the parent.
func ResearchSystemPrompt(in BuildSystemPromptInput) []api.SystemBlock {
	stable := stableBlocks(in)
	p := NewPrompt()
	p.AddVolatile(Section{Name: "research_mode", Content: researchModeSection})
	for _, s := range volatileSections(in) {
		p.AddVolatile(s)
	}
	volatile := p.SystemBlocks()
	out := make([]api.SystemBlock, 0, len(stable)+len(volatile))
	out = append(out, stable...)
	out = append(out, volatile...)
	return out
}

const planModeSection = `# Plan mode

You are running as the plan agent. Your job is to produce a written plan that the build agent can execute. You can read freely, fan out parallel investigations via the ` + "`task`" + ` tool (read-only subagents only), and write to a single plan file under ` + "`.prompto/plans/`" + `. Edit/Write are denied for any other path. Bash commands prompt for confirmation.

## Workflow

Follow these phases. Don't skip them.

1. **Initial Understanding** — Understand the user's request and the code involved. When the work spans more than one area of the codebase, use the ` + "`task`" + ` tool with ` + "`subagent_type: \"explore\"`" + ` to fan out 1–3 parallel investigations. Single-file or narrow tasks: just read directly.

2. **Design** — Decide the approach. Reference existing code paths and conventions. Pick the simplest approach that solves the actual problem; reject ones that over-generalize.

3. **Review** — Re-read what you've drafted. Confirm each section satisfies the schema below; that referenced files exist (no hallucinations); that the verification step would actually catch a wrong implementation.

4. **Final Plan** — Pick a short slug describing this work (2–5 hyphenated lowercase words) and write your plan to ` + "`.prompto/plans/YYYY-MM-DD-<slug>.md`" + ` using today's date. Use the ` + "`write`" + ` tool to create the file; use ` + "`edit`" + ` to refine. The first ` + "`write`" + ` to a plan path records it as this session's plan file — subsequent turns reference that path automatically.

5. **Exit** — When the plan satisfies the schema, call the ` + "`plan_exit`" + ` tool. Prompto will validate the plan, render it for the user, and on approval auto-switch to the build agent which executes it. If validation fails or the user rejects, you'll see a tool result explaining what to fix; revise the plan and call ` + "`plan_exit`" + ` again.

## Plan schema

The plan markdown MUST have these ` + "`##`" + ` headings (in any order; additional headings are allowed):

- ` + "`## Context`" + ` — why this change is being made; the problem or need it addresses.
- ` + "`## Goal & acceptance criteria`" + ` — what "done" looks like.
- ` + "`## Files`" + ` — what gets created or modified, with a one-line rationale each. Reference existing functions and utilities to reuse, with ` + "`file:line`" + ` citations.
- ` + "`## Verification`" + ` — how to confirm the change works end-to-end (commands, tests, manual smoke).
- ` + "`## Risks / out-of-scope`" + ` — what could break, and what this plan deliberately doesn't cover.

## Rubric

- **Outcomes, not steps.** Describe what should be true when the work is done, not the keystrokes.
- **Reference existing patterns.** If the codebase already does X, do X. Cite ` + "`file:line`" + `.
- **Think about scale and error scenarios.** What happens when the input is empty, large, or malformed? When a step fails partway?
- **Verification is part of the plan.** A plan without a check is a wish.
- **Be specific.** Names of functions, files, types — not "the relevant module".`

const exploreModeSection = `# Explore mode
You are a read-only subagent spawned by the build agent to investigate one specific question. You have grep, glob, list, read, and webfetch — nothing that writes. The build agent cannot see your tool output; only your final assistant message reaches it.

Be concise:
- Investigate quickly. Do not narrate every step.
- Return a tight summary: bullet points or a short paragraph. Reference specific files with file:line citations when relevant.
- Do not propose follow-up work; just report what you found.
- Do not include preambles like "I will investigate…". Start with findings.`

const researchModeSection = "# Research mode\n" +
	"You are a read-only subagent spawned to investigate one online question. You have websearch (discover URLs), webfetch (retrieve a page), and read-only code tools (read, grep, glob). The parent cannot see your tool output; only your final assistant message reaches it.\n" +
	"\n" +
	"Workflow:\n" +
	"1. Search broadly first. Use websearch with a 5–10 word query that names the framework, library, or version where relevant (e.g. 'React useEffect cleanup' beats 'useEffect').\n" +
	"2. Pick the most authoritative result (official docs > maintainer blog > Stack Overflow > tutorial) and webfetch it.\n" +
	"3. If the page is a SPA shell, the headless escalation handles it automatically.\n" +
	"4. Cross-check non-trivial claims across at least two sources.\n" +
	"5. When the codebase context matters (e.g. \"what version of X are we on?\"), use read/grep/glob to ground your answer.\n" +
	"\n" +
	"Be concise:\n" +
	"- Return a tight summary with bullet points.\n" +
	"- Cite every claim with the URL it came from. Use `[short label](url)`.\n" +
	"- Note the date or version of the source if it's load-bearing.\n" +
	"- Do not propose follow-up work; just report what you found.\n" +
	"- Do not include preambles. Start with the answer."

// stableSections returns the cacheable prefix content tailored to the
// agent's tool allowlist and provider profile. Sub-blocks that mention
// specific tools are filtered when those tools aren't in the allowlist;
// the anti-injection warning is included only for local providers.
//
// Ordering matters: proactiveness precedes tool-use rules because the
// "investigate rather than ask" framing needs to set the default
// disposition before the model reads the catalogue of tools.
func stableSections(in BuildSystemPromptInput) []Section {
	out := []Section{
		{Name: "identity", Content: identitySection},
		{Name: "proactiveness", Content: proactivenessSection},
		{Name: "conventions", Content: conventionsSection},
		{Name: "tool_use_rules", Content: buildToolUseRules(in)},
		{Name: "workflow", Content: workflowSection},
		{Name: "tone_and_style", Content: toneAndStyleSection},
		// Cwd/Platform/Model/Date are session-stable — they don't shift
		// between turns the way ProjectInstructions/Todos do, so they
		// live in the cacheable prefix.
		{Name: "environment", Content: renderEnvironment(in)},
	}
	return out
}

// volatileSections returns the post-boundary content. Safe to change between
// turns — the prompt cache will still hit on the stable prefix.
func volatileSections(in BuildSystemPromptInput) []Section {
	var out []Section
	if strings.TrimSpace(in.ProjectInstructions) != "" {
		out = append(out, Section{
			Name:    "project_instructions",
			Content: "# Project instructions\n\n" + strings.TrimRight(in.ProjectInstructions, "\n"),
		})
	}
	if in.WorkspaceSummary.Present() {
		out = append(out, Section{
			Name:    "workspace_summary",
			Content: in.WorkspaceSummary.PromptText(),
		})
	}
	if in.VerificationHint.Present() {
		out = append(out, Section{
			Name:    "verification",
			Content: in.VerificationHint.PromptText(),
		})
	}
	if len(in.Todos) > 0 {
		if rendered := renderTodos(in.Todos); rendered != "" {
			out = append(out, Section{
				Name:    "todos",
				Content: rendered,
			})
		}
	}
	return out
}

func renderEnvironment(in BuildSystemPromptInput) string {
	// Render cwd with forward slashes regardless of host OS. On Windows
	// the native form (`G:\Go Workspace\prompto`) is dense with
	// backslashes that the model has to JSON-escape (`\\`) when it
	// embeds the path in tool arguments. Smaller / open-weights models
	// regularly drop one of the two slashes, producing wire-format
	// strings like `"path":"G:\Go..."` — invalid JSON whose escape
	// recovery sometimes coincidentally produces `\n` (newline), which
	// then decodes successfully and lands as a control character in the
	// resolved path. Rendering `G:/Go Workspace/prompto` removes the
	// escaping hazard entirely; Go's filepath layer accepts forward
	// slashes on every OS.
	return fmt.Sprintf(`# Environment
- Working directory: %s
- Platform: %s
- Model: %s
- Date: %s`, filepath.ToSlash(in.Cwd), in.Platform, in.Model, in.Date)
}

const identitySection = `You are prompto, an interactive CLI coding agent.
You help users with software engineering tasks — writing code, debugging, refactoring, reviewing, and explaining codebases.`

const proactivenessSection = `# Proactiveness
- When the user asks you to do something, do it. Investigate with the tools you have rather than asking for details the tools can discover. "Research this project" means glob the root, read README/AGENTS.md, and grep for what's there — not ask the user to name the project.
- Use read, grep, and glob extensively. They are cheap. Parallel tool calls are supported — batch independent lookups in a single turn.
- For multi-source web research ("research X", "find best practices for Y", "compare A vs B"), spawn a research subagent via the task tool instead of calling websearch yourself — it returns a cited summary and keeps your context tight.
- For in-depth exploration tasks spanning many files ("explore the codebase", "audit the codebase"), spawn an explore subagent via the task tool instead of reading a large part of the codebase yourself — it returns a cited summary and keeps your context tight.
- For destructive or hard-to-reverse actions (delete files, overwrite without reading, force-push, rm -rf, drop database tables), confirm with the user first. For investigative actions (reading, searching, listing), just act.`

const conventionsSection = `# Conventions
- Match existing patterns. Before editing a file, read it and its neighbors; mirror style, naming, and structure. Where two places already do X, do X.
- Don't assume a library is available. Check go.mod / package.json / actual imports in nearby files before adding a new dependency.`

// buildToolUseRules assembles the Tool Use section with sub-blocks
// conditionally included based on the agent's tool allowlist and
// provider profile. The intro and Local-vs-Web/File-Reading-and-Editing
// sub-blocks are universal; sub-blocks for specific tool families
// (web fetching, shell) are emitted only when those tools are present.
//
// The anti-injection warning (toolCallChannelSection) is emitted only
// when the active provider looks local — cloud Anthropic/OpenAI never
// emit textual tool calls, so the warning is pure noise there.
func buildToolUseRules(in BuildSystemPromptInput) string {
	has := newToolSet(in.Tools)
	var b strings.Builder
	b.WriteString(toolUseIntroSection)
	if in.LocalProvider {
		b.WriteString("\n\n")
		b.WriteString(toolCallChannelSection)
	}
	b.WriteString("\n\n")
	b.WriteString(localVsWebSection)
	b.WriteString("\n\n")
	b.WriteString(fileReadingSection)
	if has("grep") || has("glob") {
		b.WriteString("\n\n")
		b.WriteString(localSearchSection)
	}
	if has("webfetch") {
		b.WriteString("\n\n")
		b.WriteString(webFetchSection)
	}
	if has("bash") {
		b.WriteString("\n\n")
		b.WriteString(shellCommandsSection)
	}
	return b.String()
}

// newToolSet returns a closure that reports whether tool name n is in
// the agent's allowlist. An empty/nil allowlist means "all available"
// — the closure returns true for every n.
func newToolSet(tools []string) func(string) bool {
	if len(tools) == 0 {
		return func(string) bool { return true }
	}
	set := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		set[t] = struct{}{}
	}
	return func(n string) bool {
		_, ok := set[n]
		return ok
	}
}

const toolUseIntroSection = `# Tool Use
You have access to tools that let you interact with the user's codebase and environment.`

const toolCallChannelSection = `## CRITICAL: Tool-call channel
- ALWAYS use the structured tool-calling API (the host expects function-call objects with name + arguments).
- NEVER emit textual tool calls inside your response or your reasoning — patterns like ` + "`<tool_call><function=…>…</function></tool_call>`" + ` or ` + "`<function_calls>…</function_calls>`" + ` are NOT executed; they are treated as plain text and your turn ends without the tool running.`

const localVsWebSection = `## Local tools vs Web tools
- read, edit, write, grep, glob, list → LOCAL filesystem only. NEVER pass a URL to these tools.
- webfetch, webfetch_headless → REMOTE URLs only. Use these for anything on the web.`

const fileReadingSection = `## File Reading and Editing
- ALWAYS read a file before editing it. The edit tool requires an exact string match from the current file contents.
- Use read to examine local files. Use offset and limit for large files.
- Use edit to modify existing files. Provide enough surrounding context in old_string to make the match unique.
- Use write to create new files. Do not use write to modify existing files — use edit instead.
- Never re-read a file unless it may have changed since you last read it.`

const localSearchSection = `## Local Searching and Navigation
- Use grep to search file contents with regex. Fast, respects .gitignore.
- Use glob to find files by name pattern (e.g. **/*.go). Results sorted by mtime. Run ` + "`glob \"**/*\"`" + ` once on a fresh repo to orient.`

const webFetchSection = `## Web Fetching
- Use webfetch for any URL — docs, repos, search results.
- For structured queries (lists, fields, version metadata) on services with a JSON API (GitHub, GitLab, npm, PyPI, crates.io, Hex), prefer the API endpoint over the human web UI. Example: ` + "`api.github.com/search/repositories?q=stars:>10000`" + ` instead of ` + "`github.com/search`" + `.
- The "query" parameter tells the summarizer what to extract — write it as a clear task ("Extract the project description and installation instructions"), not just keywords ("bitcoin golang").
- If a page requires JavaScript rendering, use webfetch_headless. It requires Chrome/Chromium installed.`

const shellCommandsSection = `## Shell Commands
- Use bash to run commands, tests, build tools.
- Do NOT use bash for file reading (use read), searching (use grep/glob), or listing (use glob).`

const workflowSection = `# Workflow

One procedure; skip steps that don't apply to your task type (Code change / Investigation / Debugging).

1. **Orient** — read AGENTS.md / README.md if present; glob ` + "`**/*`" + ` once if you don't already know the layout.
2. **Plan if multi-step** — call ` + "`todowrite`" + ` when the task spans more than two steps. The list survives compaction; treat it as your scratchpad.
3. **For debugging**: form a hypothesis explicitly, gather evidence to confirm or reject it, then verify the fix works. Don't guess-and-edit.
4. **Gather context** — read the files you'll touch and their nearest neighbors. Parallelize independent lookups.
5. **Implement** (code changes) — minimal diff. Don't refactor surroundings or add unrequested features.
6. **Verify** — If you changed code, run the project's tests and static checks. Don't claim success on a red build.
7. **Report** — short summary with ` + "`file:line`" + ` references. No recap of what's already in the diff.`

const toneAndStyleSection = `# Tone and style
- Be concise and direct. Responses render in a terminal; prefer short answers and GitHub-flavored markdown.
- Skip preambles and summaries. When the user asks a factual question, answer it. When you finish a task, stop — don't narrate what you did.
- Prioritize technical accuracy over agreement. Correct the user when they're wrong; don't flatter.
- No emojis unless the user asks for them.
- When referencing code, use the ` + "`file:line`" + ` format so the user can jump directly to it.
- Never introduce code that exposes credentials, tokens, secrets, or keys.`
