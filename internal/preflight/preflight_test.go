package preflight_test

import (
	"bytes"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"dusk/internal/ddc/ddctest"
	"dusk/internal/preflight"
)

type fakeFS struct {
	devices []string
	open    func(path string) error
}

func (f fakeFS) Glob(string) ([]string, error) { return f.devices, nil }
func (f fakeFS) OpenRW(p string) error {
	if f.open == nil {
		return nil
	}
	return f.open(p)
}

func okDDC() *ddctest.FakeRunner {
	return &ddctest.FakeRunner{Handler: func([]string) (string, error) { return "ddcutil 2.2.5\nmore text\n", nil }}
}

func gsettings(ok bool) *ddctest.FakeRunner {
	return &ddctest.FakeRunner{Handler: func([]string) (string, error) {
		if !ok {
			return "", errors.New("No such schema")
		}
		return "false\n", nil
	}}
}

func find(rep preflight.Report, name string) preflight.Result {
	for _, r := range rep.Results {
		if r.Name == name {
			return r
		}
	}
	return preflight.Result{Name: "MISSING " + name}
}

func TestAllGood(t *testing.T) {
	rep := preflight.Check(t.Context(), preflight.Env{DDC: okDDC(), Gsettings: gsettings(true), FS: fakeFS{devices: []string{"/dev/i2c-1", "/dev/i2c-5"}}})
	if rep.Failed() || !rep.NightLight {
		t.Fatalf("expected a clean report: %+v", rep)
	}
	if d := find(rep, "ddcutil").Detail; d != "ddcutil 2.2.5" {
		t.Errorf("ddcutil detail = %q", d)
	}
	for _, r := range rep.Results {
		if r.Status != preflight.OK {
			t.Errorf("%s should be ok: %+v", r.Name, r)
		}
	}
}

func TestMissingDdcutil(t *testing.T) {
	ddc := &ddctest.FakeRunner{Handler: func([]string) (string, error) { return "", errors.New("ddcutil not found in PATH") }}
	rep := preflight.Check(t.Context(), preflight.Env{DDC: ddc, FS: fakeFS{devices: []string{"/dev/i2c-1"}}})
	r := find(rep, "ddcutil")
	if !rep.Failed() || r.Status != preflight.Fail || !strings.Contains(r.Hint, "apt install ddcutil") || !strings.Contains(r.Hint, "dnf install") {
		t.Errorf("%+v", r)
	}
}

func TestNoI2CDevices(t *testing.T) {
	rep := preflight.Check(t.Context(), preflight.Env{DDC: okDDC(), FS: fakeFS{}})
	r := find(rep, "i2c devices")
	if r.Status != preflight.Fail || !strings.Contains(r.Hint, "modprobe i2c-dev") {
		t.Errorf("%+v", r)
	}
	if find(rep, "i2c access").Name != "MISSING i2c access" {
		t.Error("access cannot be checked without devices")
	}
}

func TestI2CPermissionDenied(t *testing.T) {
	denied := fakeFS{devices: []string{"/dev/i2c-1"}, open: func(string) error { return fs.ErrPermission }}
	rep := preflight.Check(t.Context(), preflight.Env{DDC: okDDC(), FS: denied})
	r := find(rep, "i2c access")
	if r.Status != preflight.Fail || !strings.Contains(r.Hint, "usermod -aG i2c") {
		t.Errorf("%+v", r)
	}
}

func TestPartialI2CAccessIsOnlyAWarning(t *testing.T) {
	partial := fakeFS{devices: []string{"/dev/i2c-1", "/dev/i2c-2"}, open: func(p string) error {
		if p == "/dev/i2c-1" {
			return fs.ErrPermission
		}
		return nil
	}}
	rep := preflight.Check(t.Context(), preflight.Env{DDC: okDDC(), FS: partial})
	if rep.Failed() || find(rep, "i2c access").Status != preflight.Warn {
		t.Errorf("%+v", rep.Results)
	}
}

func TestNightLightIsOptional(t *testing.T) {
	for name, env := range map[string]preflight.Env{
		"nil gsettings":         {DDC: okDDC(), FS: fakeFS{devices: []string{"/dev/i2c-1"}}},
		"gsettings unsupported": {DDC: okDDC(), Gsettings: gsettings(false), FS: fakeFS{devices: []string{"/dev/i2c-1"}}},
	} {
		rep := preflight.Check(t.Context(), env)
		r := find(rep, "night light")
		if rep.Failed() || rep.NightLight || r.Status != preflight.Warn || !strings.Contains(r.Hint, "DDC") {
			t.Errorf("%s: %+v failed=%v", name, r, rep.Failed())
		}
	}
}

func TestPrint(t *testing.T) {
	rep := preflight.Check(t.Context(), preflight.Env{DDC: okDDC(), FS: fakeFS{}})
	var b bytes.Buffer
	rep.Print(&b)
	for _, want := range []string{"[ ok ] ddcutil", "[FAIL] i2c devices", "sudo modprobe i2c-dev", "[warn] night light"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, b.String())
		}
	}
}

func TestOSFSGlobDoesNotPanic(t *testing.T) {
	if _, err := (preflight.OSFS{}).Glob("/nonexistent-dir-xyz/*"); err != nil {
		t.Errorf("Glob = %v", err)
	}
	if err := (preflight.OSFS{}).OpenRW("/nonexistent-dir-xyz/dev"); err == nil {
		t.Error("opening a missing path must fail")
	}
}
