package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dusk/internal/config"
	"dusk/internal/ddc/ddctest"
	"dusk/internal/prompt"
)

// ---- fakes: nothing here touches real monitors, gsettings or /dev ----

type fakeFS struct{ devices []string }

func (f fakeFS) Glob(string) ([]string, error) { return f.devices, nil }
func (fakeFS) OpenRW(string) error             { return nil }

type rig struct {
	t        *testing.T
	ddc      *ddctest.FakeRunner
	gs       *ddctest.FakeRunner
	path     string
	out      bytes.Buffer
	devices  []string
	detect   string
	noRGBBus string // bus whose RGB reads fail
	noDDC    bool
	noNight  bool
}

func newRig(t *testing.T) *rig {
	t.Helper()
	data, err := os.ReadFile("../ddc/testdata/detect_brief.txt")
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, path: filepath.Join(t.TempDir(), "cfg", "config.toml"), devices: []string{"/dev/i2c-5"}, detect: string(data)}
	r.ddc = &ddctest.FakeRunner{Handler: func(a []string) (string, error) {
		switch a[0] {
		case "--version":
			if r.noDDC {
				return "", errors.New("ddcutil not found in PATH")
			}
			return "ddcutil 2.2.5\n", nil
		case "detect":
			return r.detect, nil
		case "getvcp":
			switch a[1] {
			case "10":
				return "VCP 10 C 30 100\n", nil
			default:
				if a[3] == r.noRGBBus {
					return "", errors.New("unsupported VCP")
				}
				return "VCP " + a[1] + " C 100 100\n", nil
			}
		}
		return "", errors.New("unexpected call: " + a[0])
	}}
	r.gs = &ddctest.FakeRunner{Handler: func(a []string) (string, error) {
		if r.noNight {
			return "", errors.New("No such schema")
		}
		return "false\n", nil
	}}
	return r
}

// answers joins the given answers and pads with empty lines (= "accept the default").
func answers(lines ...string) *strings.Reader {
	return strings.NewReader(strings.Join(lines, "\n") + "\n" + strings.Repeat("\n", 200))
}

func (r *rig) run(in *strings.Reader, yes bool) error {
	r.out.Reset()
	return Run(context.Background(), Options{
		ConfigPath: r.path, Yes: yes, DDC: r.ddc, Gsettings: r.gs, FS: fakeFS{devices: r.devices},
		In: in, Out: &r.out, Now: func() time.Time { return time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC) },
	})
}

func (r *rig) config() *config.Config {
	r.t.Helper()
	cfg, err := config.Load(r.path)
	if err != nil {
		r.t.Fatalf("the written config is not valid: %v", err)
	}
	return cfg
}

func (r *rig) assertReadOnly() {
	r.t.Helper()
	if n := len(r.ddc.SetCalls()); n != 0 {
		r.t.Errorf("the wizard wrote to a monitor %d time(s)", n)
	}
	for _, c := range r.gs.Calls() {
		if c[0] != "get" {
			r.t.Errorf("the wizard changed the desktop: %v", c)
		}
	}
}

func names(cfg *config.Config) []string {
	var out []string
	for _, m := range cfg.Monitors {
		out = append(out, m.Name)
	}
	return out
}

func find(cfg *config.Config, name string) *config.Monitor {
	for i := range cfg.Monitors {
		if cfg.Monitors[i].Name == name {
			return &cfg.Monitors[i]
		}
	}
	return nil
}

// ---- tests ----

func TestAcceptingEveryDefault(t *testing.T) {
	r := newRig(t)
	if err := r.run(answers(), false); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	cfg := r.config()
	if got := strings.Join(names(cfg), ","); got != "24g4,lg-ultrawide,lf24t450f" {
		t.Errorf("names = %s", got)
	}
	matches := []string{cfg.Monitors[0].Match, cfg.Monitors[1].Match, cfg.Monitors[2].Match}
	if strings.Join(matches, "|") != "24G4#SN-AOC-0001|LG ULTRAWIDE|LF24T450F#SN-SAM-0003" {
		t.Errorf("matches = %v", matches)
	}
	for _, m := range cfg.Monitors {
		if len(m.Points) != 3 || m.Points[0] != (config.Point{Minute: 8 * 60, Brightness: 70}) || m.Points[2].Brightness != 25 {
			t.Errorf("%s: preset points = %+v", m.Name, m.Points)
		}
	}
	n := cfg.Night
	if !n.Enabled || n.Start != 21*60 || n.End != 7*60 || n.Ramp != 30*time.Minute || n.BlueGain != 60 || n.Fallback != config.FallbackAuto {
		t.Errorf("night = %+v", n)
	}
	for _, want := range []string{"[ ok ] ddcutil", "Detected monitors", "24G4", "dusk run", "Wrote "} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if matches, _ := filepath.Glob(r.path + ".bak-*"); len(matches) != 0 {
		t.Errorf("no backup expected for a fresh install: %v", matches)
	}
	r.assertReadOnly()
}

func TestCustomAnswers(t *testing.T) {
	r := newRig(t)
	in := answers(
		"desk", "2", "35", // AOC: name, constant, brightness
		"", "", "", "", "", "", "", "", // LG: name, schedule, six preset prompts
		"", "3", "07:30", "60", "07:30", "99", "23:00", "15", "", // LF: custom points (one repeated time), then finish
		"n", // night mode off
	)
	if err := r.run(in, false); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	cfg := r.config()
	if got := strings.Join(names(cfg), ","); got != "desk,lg-ultrawide,lf24t450f" {
		t.Errorf("names = %s", got)
	}
	if p := cfg.Monitors[0].Points; len(p) != 1 || p[0] != (config.Point{Minute: 0, Brightness: 35}) {
		t.Errorf("desk points = %+v", p)
	}
	if p := cfg.Monitors[2].Points; len(p) != 2 || p[0] != (config.Point{Minute: 450, Brightness: 60}) || p[1] != (config.Point{Minute: 23 * 60, Brightness: 15}) {
		t.Errorf("custom points = %+v", p)
	}
	if cfg.Night.Enabled {
		t.Error("night mode should be off")
	}
	if !strings.Contains(r.out.String(), "07:30 is already used") {
		t.Errorf("a repeated key point time should be refused:\n%s", r.out.String())
	}
	r.assertReadOnly()
}

const existingTOML = `
[night]
start = "20:00"
end = "06:00"
ramp = "15m"
blue_gain = 40
fallback = "off"

[[monitor]]
name = "mine"
match = "LG ULTRAWIDE"
points = [{ time = "08:00", brightness = 55 }, { time = "20:00", brightness = 30 }]

[[monitor]]
name = "gone"
match = "SOMETHING ELSE"
points = [{ time = "00:00", brightness = 10 }]
`

func writeExisting(t *testing.T, r *rig, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExistingConfigBecomesDefaultsAndIsBackedUp(t *testing.T) {
	r := newRig(t)
	writeExisting(t, r, existingTOML)
	if err := r.run(answers(), false); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	cfg := r.config()
	if got := strings.Join(names(cfg), ","); got != "24g4,mine,lf24t450f,gone" {
		t.Fatalf("names = %s", got)
	}
	if p := find(cfg, "mine").Points; len(p) != 2 || p[0].Brightness != 55 || p[1].Brightness != 30 {
		t.Errorf("the schedule of a known monitor should be kept: %+v", p)
	}
	if find(cfg, "gone").Match != "SOMETHING ELSE" {
		t.Error("the entry of a monitor that is not connected must be preserved")
	}
	n := cfg.Night
	if n.Start != 20*60 || n.End != 6*60 || n.Ramp != 15*time.Minute || n.BlueGain != 40 || n.Fallback != config.FallbackOff {
		t.Errorf("existing night settings should be the defaults: %+v", n)
	}
	bak, _ := filepath.Glob(r.path + ".bak-*")
	if len(bak) != 1 || !strings.HasSuffix(bak[0], ".bak-20261113-120000") {
		t.Fatalf("backup = %v", bak)
	}
	if old, _ := os.ReadFile(bak[0]); string(old) != existingTOML {
		t.Error("the backup must hold the previous file exactly")
	}
	if !strings.Contains(r.out.String(), "Keeping the entries for monitors that are not connected now: gone") {
		t.Errorf("output:\n%s", r.out.String())
	}
	r.assertReadOnly()
}

func TestDecliningLeavesTheFileUntouched(t *testing.T) {
	r := newRig(t)
	writeExisting(t, r, existingTOML)
	lines := append(make([]string, 0), "", "", "", "", "", "", "", "") // 24g4
	lines = append(lines, "", "")                                      // mine: name, keep
	lines = append(lines, "", "", "", "", "", "", "", "")              // lf24t450f
	lines = append(lines, "", "", "", "", "")                          // night
	lines = append(lines, "", "", "")                                  // per-monitor color level: keep (3 monitors)
	lines = append(lines, "n")                                         // write? no
	err := r.run(answers(lines...), false)
	if !errors.Is(err, ErrDeclined) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(r.path); string(b) != existingTOML {
		t.Error("the file changed although the user declined")
	}
	if bak, _ := filepath.Glob(r.path + ".bak-*"); len(bak) != 0 {
		t.Errorf("no backup should be made when nothing is written: %v", bak)
	}
}

func TestInvalidExistingConfig(t *testing.T) {
	r := newRig(t)
	writeExisting(t, r, "garbage [")
	if err := r.run(answers("n"), false); !errors.Is(err, ErrDeclined) {
		t.Fatalf("refusing to start from scratch: %v", err)
	}
	if b, _ := os.ReadFile(r.path); string(b) != "garbage [" {
		t.Error("file must be untouched")
	}
	if err := r.run(answers("y"), false); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	_ = r.config()
	bak, _ := filepath.Glob(r.path + ".bak-*")
	if len(bak) != 1 {
		t.Fatalf("backup = %v", bak)
	}
	if old, _ := os.ReadFile(bak[0]); string(old) != "garbage [" {
		t.Error("the unusable file should still be backed up")
	}
}

func TestYesModeNeedsNoInput(t *testing.T) {
	r := newRig(t)
	if err := r.run(strings.NewReader(""), true); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	cfg := r.config()
	if len(cfg.Monitors) != 3 || !cfg.Night.Enabled {
		t.Errorf("%+v", cfg)
	}
	// Running again keeps the existing values and backs the file up.
	before, _ := os.ReadFile(r.path)
	if err := r.run(strings.NewReader(""), true); err != nil {
		t.Fatal(err)
	}
	if bak, _ := filepath.Glob(r.path + ".bak-*"); len(bak) != 1 {
		t.Errorf("backup = %v", bak)
	}
	again := r.config()
	if strings.Join(names(again), ",") != strings.Join(names(cfg), ",") {
		t.Errorf("a second --yes run changed the names: %v", names(again))
	}
	_ = before
	r.assertReadOnly()
}

func TestNoMonitors(t *testing.T) {
	r := newRig(t)
	r.detect = "No displays found.\n"
	err := r.run(answers(), false)
	if !errors.Is(err, ErrNoMonitors) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(r.out.String(), "DDC/CI is enabled") {
		t.Errorf("the hint is missing:\n%s", r.out.String())
	}
	if _, err := os.Stat(r.path); !errors.Is(err, os.ErrNotExist) {
		t.Error("nothing should be written")
	}
}

func TestPreflightFailureStopsTheWizard(t *testing.T) {
	r := newRig(t)
	r.noDDC = true
	err := r.run(answers(), false)
	if !errors.Is(err, ErrPreflight) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(r.out.String(), "sudo apt install ddcutil") {
		t.Errorf("the fix is missing:\n%s", r.out.String())
	}
	if len(r.ddc.Calls()) != 1 {
		t.Errorf("no monitor discovery should happen after a failed preflight: %v", r.ddc.Calls())
	}
	r.devices = nil
	r.noDDC = false
	if err := r.run(answers(), false); !errors.Is(err, ErrPreflight) || !strings.Contains(r.out.String(), "modprobe i2c-dev") {
		t.Errorf("missing i2c devices: %v\n%s", err, r.out.String())
	}
}

func TestMonitorWithoutRGBAsksAboutTheFallback(t *testing.T) {
	r := newRig(t)
	r.noRGBBus = "8"
	// 3 monitors x 8 prompts, 5 night prompts, then the fallback question, then the confirmation.
	lines := append(make([]string, 24+5), "n")
	if err := r.run(answers(lines...), false); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	if !strings.Contains(r.out.String(), "cannot change color through DDC: LF24T450F") {
		t.Errorf("output:\n%s", r.out.String())
	}
	if got := r.config().Night.Fallback; got != config.FallbackOff {
		t.Errorf("fallback = %q; the user said no", got)
	}
	r.assertReadOnly()
}

func TestMonitorWithoutRGBAndWithoutANightLight(t *testing.T) {
	r := newRig(t)
	r.noRGBBus = "8"
	r.noNight = true
	if err := r.run(answers(), false); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	if !strings.Contains(r.out.String(), "will not get night mode") {
		t.Errorf("the limitation should be explained:\n%s", r.out.String())
	}
	if !strings.Contains(r.out.String(), "[warn] night light") {
		t.Errorf("preflight should warn:\n%s", r.out.String())
	}
}

func TestSameModelWithoutSerialSharesOneEntry(t *testing.T) {
	r := newRig(t)
	r.detect = "Display 1\n   I2C bus: /dev/i2c-5\n   DRM connector: card1-DP-1\n   Monitor: AOC:24G4:\n\n" +
		"Display 2\n   I2C bus: /dev/i2c-6\n   DRM connector: card1-DP-2\n   Monitor: AOC:24G4:\n"
	if err := r.run(answers(), false); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	cfg := r.config()
	if len(cfg.Monitors) != 1 || cfg.Monitors[0].Match != "24G4" {
		t.Errorf("monitors = %+v", cfg.Monitors)
	}
	if !strings.Contains(r.out.String(), "2 monitors report the same identity \"24G4\"") {
		t.Errorf("the shared identity should be explained:\n%s", r.out.String())
	}
}

func TestNightWindowIsValidatedAndAskedAgain(t *testing.T) {
	r := newRig(t)
	lines := make([]string, 24)
	lines = append(lines,
		"",                            // enable
		"21:00", "21:10", "30m", "60", // a 10-minute window cannot hold a 30-minute fade
		"21:00", "07:00", "30m", "60", // second attempt
	)
	if err := r.run(answers(lines...), false); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	if !strings.Contains(r.out.String(), "that night window is not valid") {
		t.Errorf("output:\n%s", r.out.String())
	}
	if n := r.config().Night; n.End != 7*60 {
		t.Errorf("night = %+v", n)
	}
}

func TestEndOfInputAborts(t *testing.T) {
	r := newRig(t)
	err := r.run(strings.NewReader(""), false)
	if !errors.Is(err, prompt.ErrAborted) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(r.path); !errors.Is(err, os.ErrNotExist) {
		t.Error("nothing should be written")
	}
}

func TestSlugAndUniqueName(t *testing.T) {
	for in, want := range map[string]string{
		"LG ULTRAWIDE": "lg-ultrawide", "24G4": "24g4", "  Odd__Name!! 27\" ": "odd-name-27", "!!!": "monitor", "": "monitor",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q; want %q", in, got, want)
		}
	}
	used := map[string]bool{"dell": true, "dell-2": true}
	if got := uniqueName("dell", used, ""); got != "dell-3" {
		t.Errorf("uniqueName = %q", got)
	}
	if got := uniqueName("dell", used, "dell"); got != "dell" {
		t.Errorf("a monitor keeps its own name: %q", got)
	}
}

func TestPerMonitorColorLevels(t *testing.T) {
	r := newRig(t)
	lines := make([]string, 24+5) // three monitors with defaults, then the global night mode
	lines = append(lines,
		"",             // AOC: keep the global level
		"y", "45", "n", // LG: own blue level 45, no red/green
		"y", "70", "y", "95", "90", // Samsung: blue 70, and red 95, green 90
	)
	if err := r.run(answers(lines...), false); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	cfg := r.config()
	if !cfg.Monitors[0].Night.IsZero() {
		t.Errorf("the first monitor should inherit: %+v", cfg.Monitors[0].Night)
	}
	lg := cfg.Monitors[1].Night
	if lg.BlueGain == nil || *lg.BlueGain != 45 || lg.RedGain != nil || lg.GreenGain != nil {
		t.Errorf("lg override = %+v", lg)
	}
	sam := cfg.Monitors[2].Night
	if sam.BlueGain == nil || *sam.BlueGain != 70 || sam.RedGain == nil || *sam.RedGain != 95 || sam.GreenGain == nil || *sam.GreenGain != 90 {
		t.Errorf("samsung override = %+v", sam)
	}
	if cfg.Night.BlueGain != 60 {
		t.Errorf("the global level must not change: %+v", cfg.Night)
	}
	if !strings.Contains(r.out.String(), "Each monitor can have its own night color level") {
		t.Errorf("the question should be introduced:\n%s", r.out.String())
	}
	r.assertReadOnly()
}

func TestExistingColorLevelIsKeptAndCanBeRemoved(t *testing.T) {
	r := newRig(t)
	writeExisting(t, r, existingTOML+"\n")
	data, _ := os.ReadFile(r.path)
	withOverride := strings.Replace(string(data), `match = "LG ULTRAWIDE"`, `match = "LG ULTRAWIDE"`+"\n"+`night = { blue_gain = 35 }`, 1)
	if err := os.WriteFile(r.path, []byte(withOverride), 0o644); err != nil {
		t.Fatal(err)
	}
	// All defaults: the override must survive (Enter on "different level?" keeps it, since the default is yes).
	if err := r.run(answers(), false); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	mine := find(r.config(), "mine")
	if mine.Night.BlueGain == nil || *mine.Night.BlueGain != 35 {
		t.Fatalf("the existing override was lost: %+v", mine.Night)
	}
	// Answering "n" for that monitor removes it. Order of monitors: 24g4, mine, lf24t450f.
	// The first run wrote all three monitors, so each now has an existing entry: name + keep = 2 prompts each.
	lines := make([]string, 3*2+5)     // three monitors, then the night block
	lines = append(lines, "", "n", "") // color levels: 24g4 default, mine -> no, lf24t450f default
	if err := r.run(answers(lines...), false); err != nil {
		t.Fatalf("%v\n%s", err, r.out.String())
	}
	if !find(r.config(), "mine").Night.IsZero() {
		t.Errorf("answering no should remove the override\n%s", r.out.String())
	}
}

func TestMonitorsWithoutColorControlAreNotAskedAboutColorLevels(t *testing.T) {
	r := newRig(t)
	r.noRGBBus = "8"
	r.noNight = true
	if err := r.run(answers(), false); err != nil {
		t.Fatal(err)
	}
	if strings.Count(r.out.String(), "different night color level for") != 2 {
		t.Errorf("only the two monitors with RGB should be asked:\n%s", r.out.String())
	}
}

func TestNoColorLevelQuestionsWhenNightModeIsOff(t *testing.T) {
	r := newRig(t)
	lines := make([]string, 24)
	lines = append(lines, "n") // night mode off
	if err := r.run(answers(lines...), false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.out.String(), "different night color level") {
		t.Error("nothing to ask when night mode is off")
	}
}
