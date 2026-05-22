package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// historyDirName is the subdirectory under `.prompto/plans/` where
// pre-write snapshots live. Hidden so a casual `ls` doesn't surface
// every revision; PlanRules' `*.md` glob also excludes this subtree
// so the model can't write here directly.
const historyDirName = ".history"

// BackupPlan copies an existing plan file to its `.history/` sibling
// before the next overwrite. It is invoked from the run-loop's
// pre-dispatch hook for every plan-mode `write` / `edit` call that
// targets a plan path.
//
// Behaviour:
//
//   - Source missing → nil. The first `write` of a plan has nothing
//     to back up.
//   - Source exists → copy to `.history/<stem>.<unix-ms>.md`. The
//     `<stem>` is the source basename with `.md` stripped; the
//     timestamp is captured at backup time so file ordering matches
//     write order even on filesystems with coarse mtime resolution.
//   - Sub-millisecond collision (two writes within the same ms) →
//     suffix `-1`, `-2`, … until a free filename appears. Bounded
//     loop (1024 iterations) defends against pathological clock
//     stalls; in practice the first or second probe wins.
//
// Errors are wrapped with the operation that failed. The caller
// emits `EventError` and continues; backup is best-effort.
func BackupPlan(planPath string) error {
	if planPath == "" {
		return errors.New("agent: BackupPlan: empty path")
	}
	body, err := os.ReadFile(planPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("agent: BackupPlan: read source: %w", err)
	}

	plansDir := filepath.Dir(planPath)
	historyDir := filepath.Join(plansDir, historyDirName)
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return fmt.Errorf("agent: BackupPlan: mkdir history: %w", err)
	}

	stem := strings.TrimSuffix(filepath.Base(planPath), ".md")
	ms := time.Now().UnixMilli()
	target := filepath.Join(historyDir, stem+"."+strconv.FormatInt(ms, 10)+".md")

	for i := 1; i < 1024; i++ {
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			break
		}
		target = filepath.Join(historyDir, stem+"."+strconv.FormatInt(ms, 10)+"-"+strconv.Itoa(i)+".md")
	}

	if err := os.WriteFile(target, body, 0o644); err != nil {
		return fmt.Errorf("agent: BackupPlan: write target: %w", err)
	}
	return nil
}

// LatestPlanBackup returns the most recent `.history/` snapshot for
// planPath, or "" when none exists. Used by `/plan diff` to compare
// the live plan against its previous version.
//
// The "most recent" entry is selected by sorting filenames by their
// embedded `.<unix-ms>` (and `-counter`) suffix descending; a missing
// `.history` directory or no matching entries returns "" without an
// error so callers can render a friendly "no prior version" message.
func LatestPlanBackup(planPath string) (string, error) {
	if planPath == "" {
		return "", errors.New("agent: LatestPlanBackup: empty path")
	}
	historyDir := filepath.Join(filepath.Dir(planPath), historyDirName)
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("agent: LatestPlanBackup: read dir: %w", err)
	}

	stem := strings.TrimSuffix(filepath.Base(planPath), ".md")
	prefix := stem + "."
	var (
		bestName string
		bestMS   int64 = -1
		bestCnt        = -1
	)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".md") {
			continue
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".md")
		// mid is either "<ms>" or "<ms>-<counter>".
		msStr, cntStr, hasCnt := strings.Cut(mid, "-")
		ms, err := strconv.ParseInt(msStr, 10, 64)
		if err != nil {
			continue
		}
		cnt := 0
		if hasCnt {
			c, err := strconv.Atoi(cntStr)
			if err != nil {
				continue
			}
			cnt = c
		}
		if ms > bestMS || (ms == bestMS && cnt > bestCnt) {
			bestMS = ms
			bestCnt = cnt
			bestName = name
		}
	}
	if bestName == "" {
		return "", nil
	}
	return filepath.Join(historyDir, bestName), nil
}
