package agent

import (
	"encoding/json"
	"strings"
)

const (
	readLoopWarningBody = "You've made several read/search/navigation calls in a row. Produce findings now, or make the next action decisive. Choose exactly one next action: read, grep/glob, edit/replace_lines, bash verification, or final answer."
	readLoopHardBody    = "You've made many read/search/navigation calls without editing, running bash, or giving a final answer. Stop broad discovery; make the next action decisive and grounded in what you already found. Choose exactly one next action: read, grep/glob, edit/replace_lines, bash verification, or final answer."
	editSpiralBody      = "Repeated edits to the same file failed. Re-read the file, then use a larger exact match, batch edit, or replace_lines so the replacement matches current content. Choose exactly one next action: read, grep/glob, edit/replace_lines, bash verification, or final answer."
	greetingLoopBody    = "Continue from the tool results. Do not ask how you can help. Choose exactly one next action: read, grep/glob, edit/replace_lines, bash verification, or final answer."
)

type loopGuardAction string

const (
	loopGuardActionCache      loopGuardAction = "duplicate_cached_result"
	loopGuardActionReadWarn   loopGuardAction = "read_loop_warning"
	loopGuardActionReadHard   loopGuardAction = "read_loop_hard_nudge"
	loopGuardActionEditSpiral loopGuardAction = "edit_spiral"
	loopGuardActionGreeting   loopGuardAction = "greeting_continuation"
)

type cachedToolResult struct {
	content string
	summary string
}

type loopGuardCacheEntry struct {
	key string
	res cachedToolResult
}

type LoopGuard struct {
	cache []loopGuardCacheEntry

	consecutiveReads int
	editFailures     map[string]int
	sawToolActivity  bool
	greetingNudged   bool
	actions          []string
}

func NewLoopGuard() *LoopGuard {
	return &LoopGuard{editFailures: make(map[string]int)}
}

func (g *LoopGuard) TakeActions() []string {
	if g == nil || len(g.actions) == 0 {
		return nil
	}
	out := g.actions
	g.actions = nil
	return out
}

func (g *LoopGuard) MaybeUseCached(plan *toolCallPlan) bool {
	if g == nil || plan == nil || !cacheablePlan(plan) {
		return false
	}
	key := toolCacheKey(plan.acc.name, plan.argsStr)
	if key == "" {
		return false
	}
	for i := len(g.cache) - 1; i >= 0; i-- {
		if g.cache[i].key == key {
			plan.resultContent = "[cached duplicate result]\n" + g.cache[i].res.content
			plan.resultSummary = g.cache[i].res.summary
			plan.guardSkipDispatch = true
			g.recordAction(loopGuardActionCache)
			return true
		}
	}
	return false
}

func (g *LoopGuard) RecordPlanResult(plan *toolCallPlan, queue func(string)) {
	if g == nil || plan == nil || plan.denied != "" {
		return
	}
	if !plan.resultIsError {
		g.sawToolActivity = true
	}
	if cacheablePlan(plan) && !plan.resultIsError && !plan.guardSkipDispatch {
		g.remember(plan)
	}
	g.recordReadLoop(plan.acc.name, queue)
	if (strings.EqualFold(plan.acc.name, "edit") || strings.EqualFold(plan.acc.name, "replace_lines")) && plan.resultIsError {
		g.recordEditFailure(plan.argsStr, queue)
	}
}

func (g *LoopGuard) RecordFinalText(text string, queue func(string)) bool {
	if g == nil {
		return false
	}
	g.consecutiveReads = 0
	if g.sawToolActivity && !g.greetingNudged && looksLikeGreetingRegression(text) {
		g.greetingNudged = true
		g.recordAction(loopGuardActionGreeting)
		if queue != nil {
			queue(greetingLoopBody)
		}
		return true
	}
	return false
}

func (g *LoopGuard) remember(plan *toolCallPlan) {
	key := toolCacheKey(plan.acc.name, plan.argsStr)
	if key == "" {
		return
	}
	for i, entry := range g.cache {
		if entry.key == key {
			g.cache = append(g.cache[:i], g.cache[i+1:]...)
			break
		}
	}
	g.cache = append(g.cache, loopGuardCacheEntry{
		key: key,
		res: cachedToolResult{content: plan.resultContent, summary: plan.resultSummary},
	})
	if len(g.cache) > 5 {
		g.cache = g.cache[len(g.cache)-5:]
	}
}

func (g *LoopGuard) recordReadLoop(name string, queue func(string)) {
	if readLoopTool(name) {
		g.consecutiveReads++
		switch g.consecutiveReads {
		case 5:
			g.recordAction(loopGuardActionReadWarn)
			if queue != nil {
				queue(readLoopWarningBody)
			}
		case 8:
			g.recordAction(loopGuardActionReadHard)
			if queue != nil {
				queue(readLoopHardBody)
			}
		}
		return
	}
	if decisiveTool(name) {
		g.consecutiveReads = 0
	}
}

func (g *LoopGuard) recordEditFailure(args string, queue func(string)) {
	path := editPath(args)
	if path == "" {
		return
	}
	g.editFailures[path]++
	if g.editFailures[path] == 3 {
		g.recordAction(loopGuardActionEditSpiral)
		if queue != nil {
			queue(editSpiralBody)
		}
	}
}

func (g *LoopGuard) recordAction(action loopGuardAction) {
	g.actions = append(g.actions, string(action))
}

func cacheablePlan(plan *toolCallPlan) bool {
	if plan.tool == nil || plan.acc == nil {
		return false
	}
	if !plan.isReadOnly || !plan.isConcurrent {
		return false
	}
	switch plan.acc.name {
	case "bash", "edit", "replace_lines", "write", "todowrite", "task", "plan_exit":
		return false
	default:
		return true
	}
}

func toolCacheKey(name, args string) string {
	canon, ok := canonicalJSON(args)
	if !ok {
		return ""
	}
	return name + "\x00" + canon
}

func canonicalJSON(raw string) (string, bool) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return "", false
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func readLoopTool(name string) bool {
	switch name {
	case "read", "grep", "glob", "webfetch", "websearch":
		return true
	default:
		return false
	}
}

func decisiveTool(name string) bool {
	switch name {
	case "bash", "edit", "replace_lines", "write", "todowrite", "plan_exit":
		return true
	default:
		return false
	}
}

func editPath(args string) string {
	var v struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return ""
	}
	if v.Path != "" {
		return v.Path
	}
	return v.FilePath
}

func looksLikeGreetingRegression(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	normalized = strings.Trim(normalized, " \t\r\n.!?")
	switch normalized {
	case "how can i help", "how can i help you", "how may i help", "how may i help you":
		return true
	}
	return strings.Contains(normalized, "how can i help you today")
}
