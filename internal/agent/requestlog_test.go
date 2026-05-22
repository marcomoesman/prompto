package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcomoesman/prompto/internal/api"
)

func TestRequestLog_NilLoggerIsNoop(t *testing.T) {
	var l *RequestLogger
	if err := l.Write(RequestLogEntry{Model: "x"}); err != nil {
		t.Errorf("nil logger Write = %v, want nil", err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("nil logger Close = %v, want nil", err)
	}
}

func TestRequestLog_DebugFalseReturnsNil(t *testing.T) {
	dir := t.TempDir()
	l, err := NewRequestLogger(NewRequestLoggerInput{Debug: false, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if l != nil {
		t.Errorf("want nil logger when Debug=false, got %v", l)
	}
}

func TestRequestLog_WritesJSONLLines(t *testing.T) {
	dir := t.TempDir()
	fixed := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	l, err := NewRequestLogger(NewRequestLoggerInput{
		Debug: true,
		Dir:   dir,
		Now:   func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	entries := []RequestLogEntry{
		{Timestamp: fixed, Model: "sonnet", MsgCount: 1, DurationMs: 42},
		{Timestamp: fixed, Model: "sonnet", MsgCount: 2, Usage: &api.Usage{InputTokens: 10}, DurationMs: 100},
	}
	for _, e := range entries {
		if err := l.Write(e); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	// File must exist with the expected name.
	path := filepath.Join(dir, "requests-2026-04-21.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	for i, line := range lines {
		var parsed RequestLogEntry
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Errorf("line %d not JSON: %v", i, err)
		}
	}
}

func TestRequestLog_ConcurrentWritesOrdered(t *testing.T) {
	dir := t.TempDir()
	fixed := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	l, err := NewRequestLogger(NewRequestLoggerInput{
		Debug: true,
		Dir:   dir,
		Now:   func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	var wg sync.WaitGroup
	const n = 100
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = l.Write(RequestLogEntry{Timestamp: fixed, Model: "m", MsgCount: i})
		}(i)
	}
	wg.Wait()

	path := filepath.Join(dir, "requests-2026-04-21.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var count int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		var parsed map[string]any
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Errorf("corrupted line: %q: %v", line, err)
			continue
		}
		count++
	}
	if count != n {
		t.Errorf("got %d lines, want %d", count, n)
	}
}

func TestRequestLog_CreatesDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "nested", "dir")
	l, err := NewRequestLogger(NewRequestLoggerInput{Debug: true, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestRequestLog_EntryFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l, err := NewRequestLogger(NewRequestLoggerInput{Debug: true, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	entry := RequestLogEntry{
		Timestamp:    time.Unix(1700000000, 0),
		Model:        "sonnet-4-6",
		MsgCount:     3,
		SystemSHA256: strings.Repeat("a", 64),
		ToolNames:    []string{"read", "edit"},
		Usage:        &api.Usage{InputTokens: 50, OutputTokens: 20, CacheRead: 10},
		DurationMs:   1234,
		Error:        "",
	}
	if err := l.Write(entry); err != nil {
		t.Fatal(err)
	}
}
