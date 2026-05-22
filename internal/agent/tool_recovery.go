package agent

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

type recoveredToolCall struct {
	ID   string
	Name string
	Args string
}

type toolCallRecoveryResult struct {
	Calls []recoveredToolCall
	Text  string
}

func recoverTextualToolCalls(text string, tools ToolResolver) toolCallRecoveryResult {
	if strings.TrimSpace(text) == "" || tools == nil {
		return toolCallRecoveryResult{Text: text}
	}
	candidates := textualToolCallJSONCandidates(text)
	if len(candidates) != 1 {
		return toolCallRecoveryResult{Text: text}
	}
	calls := parseRecoveredToolCalls(candidates[0].JSON, tools)
	if len(calls) == 0 {
		return toolCallRecoveryResult{Text: text}
	}
	return toolCallRecoveryResult{
		Calls: calls,
		Text:  removeRecoveredCandidate(text, candidates[0]),
	}
}

type textualToolCallCandidate struct {
	JSON  string
	Start int
	End   int
}

func textualToolCallJSONCandidates(text string) []textualToolCallCandidate {
	var out []textualToolCallCandidate
	out = append(out, fencedJSONCandidates(text)...)
	for _, tag := range []string{"tool_call", "function_calls"} {
		out = append(out, taggedJSONCandidates(text, tag)...)
	}
	trimmed := strings.TrimSpace(text)
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		start := strings.Index(text, trimmed)
		out = append(out, textualToolCallCandidate{JSON: trimmed, Start: start, End: start + len(trimmed)})
	}
	return out
}

func fencedJSONCandidates(text string) []textualToolCallCandidate {
	var out []textualToolCallCandidate
	rest := text
	offset := 0
	for {
		start := strings.Index(rest, "```")
		if start < 0 {
			return out
		}
		afterFence := rest[start+3:]
		lineEnd := strings.IndexByte(afterFence, '\n')
		if lineEnd < 0 {
			return out
		}
		info := strings.TrimSpace(afterFence[:lineEnd])
		afterInfo := afterFence[lineEnd+1:]
		end := strings.Index(afterInfo, "```")
		if end < 0 {
			return out
		}
		body := strings.TrimSpace(afterInfo[:end])
		if info == "" || strings.EqualFold(info, "json") || strings.Contains(strings.ToLower(info), "json") {
			out = append(out, textualToolCallCandidate{
				JSON:  body,
				Start: offset + start,
				End:   offset + start + 3 + lineEnd + 1 + end + 3,
			})
		}
		advance := start + 3 + lineEnd + 1 + end + 3
		offset += advance
		rest = rest[advance:]
	}
}

func taggedJSONCandidates(text, tag string) []textualToolCallCandidate {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	var out []textualToolCallCandidate
	rest := text
	offset := 0
	for {
		lowerRest := strings.ToLower(rest)
		start := strings.Index(lowerRest, open)
		if start < 0 {
			return out
		}
		bodyStart := start + len(open)
		afterOpen := rest[bodyStart:]
		end := strings.Index(strings.ToLower(afterOpen), close)
		if end < 0 {
			return out
		}
		out = append(out, textualToolCallCandidate{
			JSON:  strings.TrimSpace(afterOpen[:end]),
			Start: offset + start,
			End:   offset + bodyStart + end + len(close),
		})
		advance := bodyStart + end + len(close)
		offset += advance
		rest = rest[advance:]
	}
}

func removeRecoveredCandidate(text string, candidate textualToolCallCandidate) string {
	if candidate.Start < 0 || candidate.End > len(text) || candidate.Start >= candidate.End {
		return text
	}
	cleaned := strings.TrimSpace(text[:candidate.Start] + text[candidate.End:])
	if cleaned == "" {
		return ""
	}
	return cleaned
}

func parseRecoveredToolCalls(candidate string, tools ToolResolver) []recoveredToolCall {
	var raw any
	if err := json.Unmarshal([]byte(candidate), &raw); err != nil {
		return nil
	}
	switch v := raw.(type) {
	case []any:
		var out []recoveredToolCall
		for _, item := range v {
			call, ok := recoveredCallFromObject(item, tools)
			if !ok {
				return nil
			}
			out = append(out, call)
		}
		return out
	case map[string]any:
		if call, ok := recoveredCallFromMap(v, tools); ok {
			return []recoveredToolCall{call}
		}
	}
	return nil
}

func recoveredCallFromObject(v any, tools ToolResolver) (recoveredToolCall, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return recoveredToolCall{}, false
	}
	return recoveredCallFromMap(m, tools)
}

func recoveredCallFromMap(m map[string]any, tools ToolResolver) (recoveredToolCall, bool) {
	name := stringValue(m["name"])
	if name == "" {
		name = stringValue(m["tool"])
	}
	if name != "" {
		args, ok := objectValue(m["arguments"])
		if !ok {
			args, ok = objectValue(m["input"])
		}
		if !ok {
			return recoveredToolCall{}, false
		}
		return buildRecoveredCall(name, args, tools)
	}

	var foundName string
	var foundArgs map[string]any
	for key, value := range m {
		if _, ok := tools.Resolve(key); !ok {
			continue
		}
		args, ok := objectValue(value)
		if !ok {
			return recoveredToolCall{}, false
		}
		if foundName != "" {
			return recoveredToolCall{}, false
		}
		foundName = key
		foundArgs = args
	}
	if foundName == "" {
		return recoveredToolCall{}, false
	}
	return buildRecoveredCall(foundName, foundArgs, tools)
}

func buildRecoveredCall(name string, args map[string]any, tools ToolResolver) (recoveredToolCall, bool) {
	if _, ok := tools.Resolve(name); !ok {
		return recoveredToolCall{}, false
	}
	data, err := json.Marshal(args)
	if err != nil || !json.Valid(data) {
		return recoveredToolCall{}, false
	}
	return recoveredToolCall{
		ID:   "recovered_" + uuid.NewString(),
		Name: name,
		Args: string(data),
	}, true
}

func stringValue(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func objectValue(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}
