package privatefs

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	DirMode  os.FileMode = 0o700
	FileMode os.FileMode = 0o600
)

func EnsureDir(path string) error {
	if err := os.MkdirAll(path, DirMode); err != nil {
		return err
	}
	return os.Chmod(path, DirMode)
}

func EnsureParent(path string) error {
	return EnsureDir(filepath.Dir(path))
}

func WriteFile(path string, data []byte) error {
	if err := EnsureParent(path); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, FileMode); err != nil {
		return err
	}
	return os.Chmod(path, FileMode)
}

func OpenFile(path string, flag int) (*os.File, error) {
	if err := EnsureParent(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, flag, FileMode)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, FileMode); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("chmod %s: %w", path, err)
	}
	return f, nil
}

func HardenFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return os.Chmod(path, FileMode)
}
