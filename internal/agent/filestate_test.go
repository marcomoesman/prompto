package agent

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFileState_PutThenCheckPasses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	content := []byte("hello")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	fs := NewFileState()
	fs.Put(path, info.ModTime(), content)

	if err := fs.Check(path); err != nil {
		t.Errorf("Check after Put: %v", err)
	}
}

func TestFileState_CheckReturnsErrReadBeforeWriteWhenAbsent(t *testing.T) {
	fs := NewFileState()
	err := fs.Check("/tmp/never-recorded")
	if !errors.Is(err, ErrReadBeforeWrite) {
		t.Errorf("err = %v, want ErrReadBeforeWrite", err)
	}
}

func TestFileState_CheckPassesWhenOnlyMtimeChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	content := []byte("same content")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	fs := NewFileState()
	fs.Put(path, info.ModTime(), content)

	// Bump mtime without changing content.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	if err := fs.Check(path); err != nil {
		t.Errorf("Check with only mtime changed = %v, want nil (content identical)", err)
	}
}

func TestFileState_CheckFailsWhenContentChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	fs := NewFileState()
	fs.Put(path, info.ModTime(), []byte("original"))

	// External overwrite with different content.
	if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ensure mtime differs (some filesystems have 1s resolution).
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	err = fs.Check(path)
	if !errors.Is(err, ErrFileChanged) {
		t.Errorf("err = %v, want ErrFileChanged", err)
	}
}

func TestFileState_CheckFailsWhenFileDeleted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	content := []byte("data")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	fs := NewFileState()
	fs.Put(path, info.ModTime(), content)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	err = fs.Check(path)
	if !errors.Is(err, ErrFileMissing) {
		t.Errorf("err = %v, want ErrFileMissing", err)
	}
}

func TestFileState_ConcurrentPutCheck(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileState()
	var wg sync.WaitGroup

	// Pre-create files and seed.
	paths := make([]string, 50)
	for i := range paths {
		p := filepath.Join(dir, "f", string(rune('a'+i%26)))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths[i] = p
	}

	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := paths[i%len(paths)]
			info, _ := os.Stat(p)
			fs.Put(p, info.ModTime(), []byte("x"))
		}(i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := paths[i%len(paths)]
			_ = fs.Check(p)
		}(i)
	}
	wg.Wait()
}

func TestFileState_HasReportsPutState(t *testing.T) {
	fs := NewFileState()
	if fs.Has("/tmp/x") {
		t.Error("Has should be false before Put")
	}
	fs.Put("/tmp/x", time.Now(), []byte("y"))
	if !fs.Has("/tmp/x") {
		t.Error("Has should be true after Put")
	}
}
