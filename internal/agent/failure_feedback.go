package agent

import (
	"fmt"
	"strings"
)

const singleNextActionInstruction = "Choose exactly one next action: read, grep/glob, edit/replace_lines, bash verification, or final answer."

func singleNextActionReminder(observed, corrective string) string {
	if corrective == "" {
		return observed + " " + singleNextActionInstruction
	}
	return observed + " " + corrective + " " + singleNextActionInstruction
}

func failureFeedbackForPlan(plan *toolCallPlan, content string, isError bool) string {
	if plan == nil || plan.acc == nil {
		return ""
	}
	name := plan.acc.name
	msg := strings.ToLower(content)

	if strings.Contains(msg, "invalid json arguments") {
		return singleNextActionReminder(
			"Tool call failed: invalid JSON arguments.",
			"Retry with a complete JSON object for the selected tool.",
		)
	}
	if strings.Contains(msg, "unknown tool") {
		return singleNextActionReminder(
			fmt.Sprintf("Tool call failed: unknown tool %q.", name),
			"Use an available tool name.",
		)
	}

	switch name {
	case "edit":
		if !isError {
			return ""
		}
		switch {
		case strings.Contains(msg, "old_string not found") || strings.Contains(msg, "not found"):
			return singleNextActionReminder(
				"Edit failed: old_string was not found.",
				"Read the current file, then retry with current exact text or use replace_lines.",
			)
		case strings.Contains(msg, "appears") && strings.Contains(msg, "times"):
			return singleNextActionReminder(
				"Edit failed: old_string matched more than once.",
				"Use more surrounding context or replace_lines.",
			)
		case staleFileFeedback(msg):
			return singleNextActionReminder(
				"Edit failed: the file state is unread, stale, or missing.",
				"Read the file before editing again.",
			)
		default:
			return singleNextActionReminder(
				"Edit failed.",
				"Read current contents, then retry edit or replace_lines.",
			)
		}
	case "replace_lines":
		if !isError {
			return ""
		}
		switch {
		case strings.Contains(msg, "beyond eof") ||
			strings.Contains(msg, "start_line") ||
			strings.Contains(msg, "end_line") ||
			strings.Contains(msg, "range"):
			return singleNextActionReminder(
				"replace_lines failed: the line range was invalid.",
				"Read current line numbers, then retry with a valid range.",
			)
		case staleFileFeedback(msg):
			return singleNextActionReminder(
				"replace_lines failed: the file state is unread, stale, or missing.",
				"Read the file before editing again.",
			)
		default:
			return singleNextActionReminder(
				"replace_lines failed.",
				"Read current line numbers, then retry with a valid range.",
			)
		}
	case "bash":
		if bashFailure(plan, content, isError) {
			return singleNextActionReminder(
				"Bash command failed.",
				"Use the output to decide the next correction or verification command.",
			)
		}
	}
	return ""
}

func staleFileFeedback(msg string) bool {
	return strings.Contains(msg, "not read before write") ||
		strings.Contains(msg, "read the file first") ||
		strings.Contains(msg, "changed since last read") ||
		strings.Contains(msg, "file missing")
}

func bashFailure(plan *toolCallPlan, content string, isError bool) bool {
	if isError {
		return true
	}
	summary := strings.ToLower(plan.resultSummary)
	msg := strings.ToLower(content)
	if strings.Contains(summary, "timed out") || strings.Contains(msg, "[command timed out") {
		return true
	}
	if strings.Contains(msg, "[exit code:") {
		return true
	}
	return strings.HasPrefix(summary, "exit ") && !strings.HasPrefix(summary, "exit 0 ")
}
