// Filename: internal/config/write_test.go
// Created on: November 17, 2026
// Author: Lucas Araújo <araujolucas@dcc.ufmg.br>

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dusk/internal/config"
)

func validConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(monitorToml("")))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestWriteCreatesTheFileWithoutABackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	backup, err := config.Write(path, validConfig(t), time.Now())
	if err != nil || backup != "" {
		t.Fatalf("backup %q err %v", backup, err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatal(err)
	}
}

func TestWriteBacksUpTheOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("# hand written\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 21, 30, 5, 0, time.UTC)
	backup, err := config.Write(path, validConfig(t), now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(backup, ".bak-20261117-213005") {
		t.Fatalf("backup = %q", backup)
	}
	if old, _ := os.ReadFile(backup); string(old) != "# hand written\n" {
		t.Errorf("backup content = %q", old)
	}
}

func TestWriteRefusesAnInvalidConfigAndLeavesTheFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig(t)
	cfg.Monitors[0].Floor = 99 // outside 0-50
	if _, err := config.Write(path, cfg, time.Now()); err == nil || !config.IsValidation(err) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "keep me" {
		t.Errorf("the file changed: %q", got)
	}
	if bak, _ := filepath.Glob(path + ".bak-*"); len(bak) != 0 {
		t.Errorf("no backup is expected for a refused write: %v", bak)
	}
}

func TestValidate(t *testing.T) {
	if err := config.Validate(validConfig(t)); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig(t)
	cfg.Night.Enabled, cfg.Night.Start, cfg.Night.End = true, 600, 600
	if err := config.Validate(cfg); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("err = %v", err)
	}
}
