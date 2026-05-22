package agent

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// File-state sentinel errors. These describe tool-result conditions, not
// termination reasons — tools wrap them and the loop emits them as
// tool_result.is_error=true so the model can recover.
var (
	ErrReadBeforeWrite = errors.New("agent: file was not read before write")
	ErrFileChanged     = errors.New("agent: file changed since last read")
	ErrFileMissing     = errors.New("agent: file missing")
)

// FileEntry captures what we recorded about a file at read time.
type FileEntry struct {
	Mtime  time.Time
	SHA256 [32]byte
}

// FileState tracks which files the agent has read during this session and
// enforces a read-before-write invariant. mtime is cheap to check; when it
// differs we re-hash to tolerate benign touches (formatters, backups) that
// don't alter content.
type FileState struct {
	mu    sync.RWMutex
	files map[string]FileEntry
}

// NewFileState returns an empty FileState ready for use.
func NewFileState() *FileState {
	return &FileState{files: make(map[string]FileEntry)}
}

// Put records that path was read at mtime with the given content. The hash is
// computed from the in-memory content to avoid a TOCTOU between the read and
// the record.
func (fs *FileState) Put(path string, mtime time.Time, content []byte) {
	if fs == nil {
		return
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	entry := FileEntry{
		Mtime:  mtime,
		SHA256: sha256.Sum256(content),
	}
	fs.files[path] = entry
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
		fs.files[resolved] = entry
	}
}

// Has reports whether path has been recorded.
func (fs *FileState) Has(path string) bool {
	if fs == nil {
		return false
	}
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	_, ok := fs.files[path]
	return ok
}

// Check verifies the recorded entry is still valid for path. Semantics:
//   - If path was never Put: ErrReadBeforeWrite.
//   - If path is now missing: ErrFileMissing.
//   - If mtime is unchanged: nil.
//   - If mtime changed but content hash matches: nil (touch without edit).
//   - If content hash differs: ErrFileChanged.
func (fs *FileState) Check(path string) error {
	if fs == nil {
		return ErrReadBeforeWrite
	}
	fs.mu.RLock()
	entry, ok := fs.files[path]
	fs.mu.RUnlock()
	if !ok {
		return ErrReadBeforeWrite
	}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrFileMissing
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}

	if info.ModTime().Equal(entry.Mtime) {
		return nil
	}

	// mtime differs — re-hash to decide whether content actually changed.
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("re-reading %s: %w", path, err)
	}
	if sha256.Sum256(content) == entry.SHA256 {
		return nil
	}
	return ErrFileChanged
}
