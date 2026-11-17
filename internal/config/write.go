// Filename: internal/config/write.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Validate reports whether cfg survives a round trip through the file format:
// the same checks that Load applies to a file on disk.
func Validate(cfg *Config) error {
	_, err := Parse(Marshal(cfg, nil))
	return err
}

// Write validates cfg and saves it to path atomically. An existing file is
// first copied to <path>.bak-<timestamp>; the backup path is returned ("" when
// there was no file). Nothing is written when cfg is invalid.
func Write(path string, cfg *Config, now time.Time) (string, error) {
	if err := Validate(cfg); err != nil {
		return "", err
	}
	data := Marshal(cfg, nil)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	backup := ""
	if old, err := os.ReadFile(path); err == nil {
		backup = path + ".bak-" + now.Format("20060102-150405")
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return "", fmt.Errorf("saving the backup: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return backup, nil
}
