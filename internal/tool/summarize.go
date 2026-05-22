package tool

import "context"

// Summarizer is a function that summarizes web page content using an LLM.
// It takes the raw markdown content and the user's query for context-aware
// summarization. Returns the summary text.
type Summarizer func(ctx context.Context, content, userQuery string) (string, error)
