package ddc_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"dusk/internal/ddc"
	"dusk/internal/ddc/ddctest"
)

func TestParseDetectFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/detect_brief.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := ddc.ParseDetect(string(data))
	want := []ddc.Info{
		{Mfg: "AOC", Model: "24G4", Serial: "SN-AOC-0001", Bus: 5, Connector: "card1-DP-1"},
		{Mfg: "GSM", Model: "LG ULTRAWIDE", Serial: "", Bus: 7, Connector: "card1-HDMI-A-1"},
		{Mfg: "SAM", Model: "LF24T450F", Serial: "SN-SAM-0003", Bus: 8, Connector: "card1-DP-3"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if got[0].Key() != "24G4#SN-AOC-0001" || got[1].Key() != "LG ULTRAWIDE" {
		t.Errorf("Key: %q %q", got[0].Key(), got[1].Key())
	}
}

func TestParseDetectIgnoresInvalid(t *testing.T) {
	out := `Invalid display
   I2C bus:          /dev/i2c-3
   EDID synopsis:
      Mfg id:        XXX

Display 1
   I2C bus:          /dev/i2c-5
   DRM connector:    card1-DP-1
   Monitor:          AOC:24G4:S1

Display 2
   I2C bus:          /dev/i2c-6
   DRM connector:    card1-DP-2
`
	got := ddc.ParseDetect(out)
	if len(got) != 1 || got[0].Bus != 5 {
		t.Errorf("got %+v", got)
	}
	if len(ddc.ParseDetect("No displays found.\n")) != 0 {
		t.Error("no monitors should give an empty list")
	}
}

// Bus 5 answers everything; bus 8 does not support RGB.
func handler(args []string) (string, error) {
	if args[0] != "getvcp" {
		return "", nil
	}
	bus := args[3]
	switch args[1] {
	case "10":
		return "VCP 10 C 30 100\n", nil
	case "16", "18", "1A":
		if bus == "8" {
			return "", errors.New("VCP not supported")
		}
		return fmt.Sprintf("VCP %s C 50 100\n", args[1]), nil
	}
	return "", errors.New("unexpected")
}

func probed(t *testing.T, bus int) (*ddc.Device, *ddctest.FakeRunner) {
	t.Helper()
	fr := &ddctest.FakeRunner{Handler: handler}
	d := ddc.NewDevice(fr, ddc.Info{Model: "M", Bus: bus})
	_ = d.Probe(context.Background())
	return d, fr
}

func TestProbeCaps(t *testing.T) {
	d, fr := probed(t, 5)
	want := ddc.Caps{Brightness: true, BrightnessMax: 100, RGB: true, RGBMax: [3]int{100, 100, 100}}
	if d.Caps() != want {
		t.Errorf("caps = %+v", d.Caps())
	}
	if len(fr.Calls()) != 4 || len(fr.SetCalls()) != 0 {
		t.Errorf("Probe must make 4 reads and no writes: %v", fr.Calls())
	}
	d8, _ := probed(t, 8)
	if c := d8.Caps(); !c.Brightness || c.RGB {
		t.Errorf("bus 8: brightness yes, RGB no; got %+v", c)
	}
}

func TestGet(t *testing.T) {
	d, fr := probed(t, 5)
	cur, max, err := d.Get(context.Background(), ddc.VCPBrightness)
	if err != nil || cur != 30 || max != 100 {
		t.Fatalf("Get = %d,%d,%v", cur, max, err)
	}
	last := fr.Calls()[len(fr.Calls())-1]
	if !reflect.DeepEqual(last, []string{"getvcp", "10", "--bus", "5", "--brief"}) {
		t.Errorf("arguments: %v", last)
	}
}

func TestSetWhitelistAndClamp(t *testing.T) {
	d, fr := probed(t, 5)
	ctx := context.Background()

	if err := d.Set(ctx, ddc.VCP(0x14), 5); !errors.Is(err, ddc.ErrVCPNotAllowed) {
		t.Errorf("0x14 should be refused: %v", err)
	}
	if err := d.Set(ctx, ddc.VCP(0x0C), 5); !errors.Is(err, ddc.ErrVCPNotAllowed) {
		t.Errorf("0x0C should be refused: %v", err)
	}
	if n := len(fr.SetCalls()); n != 0 {
		t.Fatalf("no setvcp expected, got %d", n)
	}

	for _, tc := range []struct {
		vcp  ddc.VCP
		in   int
		want string
	}{
		{ddc.VCPBrightness, 150, "100"},
		{ddc.VCPBrightness, -5, "0"},
		{ddc.VCPBlueGain, 42, "42"},
	} {
		if err := d.Set(ctx, tc.vcp, tc.in); err != nil {
			t.Fatal(err)
		}
		c := fr.SetCalls()
		got := c[len(c)-1]
		want := []string{"setvcp", fmt.Sprintf("%02X", byte(tc.vcp)), tc.want, "--bus", "5"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Set(%v,%d): %v; want %v", tc.vcp, tc.in, got, want)
		}
	}
	for _, c := range fr.SetCalls() {
		if !ddc.VCP(mustHex(t, c[1])).Allowed() {
			t.Errorf("write outside the whitelist: %v", c)
		}
	}
}

func mustHex(t *testing.T, s string) byte {
	t.Helper()
	var b byte
	if _, err := fmt.Sscanf(s, "%02X", &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSetUnsupportedAndUnprobed(t *testing.T) {
	d8, fr := probed(t, 8)
	if err := d8.Set(context.Background(), ddc.VCPBlueGain, 50); !errors.Is(err, ddc.ErrUnsupported) {
		t.Errorf("RGB on bus 8: %v", err)
	}
	fresh := ddc.NewDevice(fr, ddc.Info{Bus: 9})
	before := len(fr.Calls())
	if err := fresh.Set(context.Background(), ddc.VCPBrightness, 50); !errors.Is(err, ddc.ErrUnsupported) {
		t.Errorf("without probing: %v", err)
	}
	if len(fr.Calls()) != before {
		t.Error("must not call ddcutil without probing")
	}
}

func TestSetPropagatesRunnerError(t *testing.T) {
	boom := errors.New("boom")
	fr := &ddctest.FakeRunner{Handler: func(a []string) (string, error) {
		if a[0] == "setvcp" {
			return "", boom
		}
		return handler(a)
	}}
	d := ddc.NewDevice(fr, ddc.Info{Bus: 5})
	_ = d.Probe(context.Background())
	if err := d.Set(context.Background(), ddc.VCPBrightness, 10); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestGetBadResponses(t *testing.T) {
	for name, out := range map[string]string{
		"empty":          "",
		"non-continuous": "VCP 14 SNC x05\n",
		"bad values":     "VCP 10 C a b\n",
		"no VCP line":    "something else\n",
	} {
		fr := &ddctest.FakeRunner{Handler: func([]string) (string, error) { return out, nil }}
		d := ddc.NewDevice(fr, ddc.Info{Bus: 1})
		if _, _, err := d.Get(context.Background(), ddc.VCPBrightness); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// These tests run harmless system binaries, never ddcutil.
func TestExecRunner(t *testing.T) {
	out, err := ddc.ExecRunner{Path: "echo"}.Run(context.Background(), "a", "b")
	if err != nil || out != "a b\n" {
		t.Errorf("echo: %q %v", out, err)
	}
	_, err = ddc.ExecRunner{Path: "dusk-nonexistent-binary"}.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing binary: %v", err)
	}
	_, err = ddc.ExecRunner{Path: "sleep", Timeout: 50 * time.Millisecond}.Run(context.Background(), "5")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("timeout: %v", err)
	}
	_, err = ddc.ExecRunner{Path: "false"}.Run(context.Background())
	if err == nil {
		t.Error("a non-zero exit must become an error")
	}
}
