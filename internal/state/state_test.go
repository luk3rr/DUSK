package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "sub", "state.json")}
	st := New()
	st.Originals["24G4#S1"] = RGB{R: 90, G: 95, B: 100}
	if err := s.Save(st); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Originals["24G4#S1"] != (RGB{90, 95, 100}) || got.Version != Version {
		t.Errorf("round trip: %+v", got)
	}
}

func TestSaveIsPrivateAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	s := Store{Path: filepath.Join(dir, "state.json")}
	for i := 0; i < 3; i++ {
		if err := s.Save(New()); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	st, err := Store{Path: filepath.Join(t.TempDir(), "missing.json")}.Load()
	if err != nil || len(st.Originals) != 0 {
		t.Errorf("%+v %v", st, err)
	}
}

func TestLoadBadFiles(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"corrupt": "{this is not json",
		"version": `{"version": 99, "originals": {}}`,
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		st, err := Store{Path: p}.Load()
		if err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if st.Originals == nil {
			t.Errorf("%s: should return a usable empty state", name)
		}
	}
}

func TestLoadNilOriginalsBecomesEmptyMap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(p, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Store{Path: p}.Load()
	if err != nil || st.Originals == nil {
		t.Errorf("%+v %v", st, err)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/x/state")
	p, err := DefaultPath()
	if err != nil || p != "/x/state/dusk/state.json" || !strings.HasSuffix(p, "state.json") {
		t.Errorf("%q %v", p, err)
	}
}
