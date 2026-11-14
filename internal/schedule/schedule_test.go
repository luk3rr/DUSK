package schedule

import (
	"math"
	"testing"
	"time"

	"dusk/internal/config"
)

func at(h, m, s int) time.Time { return time.Date(2026, 11, 13, h, m, s, 0, time.UTC) }

func mon(pts ...config.Point) config.Monitor {
	return config.Monitor{Name: "x", Match: "X", Points: pts}
}

func pt(h, m, b int) config.Point { return config.Point{Minute: h*60 + m, Brightness: b} }

func TestTarget(t *testing.T) {
	day := mon(pt(8, 0, 70), pt(18, 0, 40), pt(22, 0, 20))
	tests := []struct {
		name string
		mon  config.Monitor
		t    time.Time
		want int
	}{
		{"exactly on a point", day, at(18, 0, 0), 40},
		{"halfway between 18 and 22", day, at(20, 0, 0), 30},
		{"one quarter", day, at(19, 0, 0), 35},
		{"after the last point, wrapping past midnight", day, at(3, 0, 0), 45}, // 22:00 to 08:00: 5h of 10h
		{"midnight", day, at(0, 0, 0), 30},                                     // 2h of 10h: 20+50*0.2
		{"before the first point", day, at(7, 0, 0), 65},
		{"single point", mon(pt(0, 0, 55)), at(13, 37, 0), 55},
		{"seconds count", mon(pt(0, 0, 0), pt(0, 10, 100)), at(0, 5, 0), 50},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Target(tc.mon, tc.t)
			if !ok || got != tc.want {
				t.Errorf("Target = %d,%v; want %d", got, ok, tc.want)
			}
		})
	}
}

func TestTargetNoPoints(t *testing.T) {
	if _, ok := Target(config.Monitor{}, at(10, 0, 0)); ok {
		t.Error("a monitor without points must return false")
	}
}

func TestTargetMonotonicBetweenPoints(t *testing.T) {
	m := mon(pt(18, 0, 40), pt(22, 0, 20))
	prev := 101
	for min := 18 * 60; min <= 22*60; min++ {
		v, _ := Target(m, at(min/60, min%60, 0))
		if v > prev {
			t.Fatalf("brightness rose at %02d:%02d (%d > %d)", min/60, min%60, v, prev)
		}
		if v < 20 || v > 40 {
			t.Fatalf("out of range at %02d:%02d: %d", min/60, min%60, v)
		}
		prev = v
	}
}

func TestTargetDSTNoLargeJump(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	m := mon(pt(0, 0, 0), pt(12, 0, 100))
	// 2026-03-08: at 02:00 the clock jumps to 03:00.
	before := time.Date(2026, 3, 8, 1, 59, 0, 0, loc)
	after := before.Add(2 * time.Minute) // 03:01 on the wall clock
	if after.Hour() != 3 {
		t.Fatalf("DST premise failed: %v", after)
	}
	a, _ := Target(m, before)
	b, _ := Target(m, after)
	if b < a || b-a > 15 { // ~1h jump = ~8 points
		t.Errorf("unexpected DST jump: %d -> %d", a, b)
	}
}

func night(startH, endH int, ramp time.Duration) config.Night {
	return config.Night{Enabled: true, Start: startH * 60, End: endH * 60, Ramp: ramp, BlueGain: 60, RedGain: 100, GreenGain: 100}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestNightIntensity(t *testing.T) {
	n := night(21, 7, 30*time.Minute)
	tests := []struct {
		name string
		t    time.Time
		want float64
	}{
		{"before start", at(20, 59, 0), 0},
		{"start", at(21, 0, 0), 0},
		{"21:15 is 50%", at(21, 15, 0), 0.5},
		{"end of ramp-up", at(21, 30, 0), 1},
		{"small hours", at(3, 0, 0), 1},
		{"end of window", at(7, 0, 0), 1},
		{"07:15 ramping down 50%", at(7, 15, 0), 0.5},
		{"end of ramp-down", at(7, 30, 0), 0},
		{"daytime", at(14, 0, 0), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NightIntensity(n, tc.t); !near(got, tc.want) {
				t.Errorf("= %v; want %v", got, tc.want)
			}
		})
	}
	off := n
	off.Enabled = false
	if NightIntensity(off, at(3, 0, 0)) != 0 {
		t.Error("disabled must give 0")
	}
}

func TestNightIntensityWindowSameDay(t *testing.T) {
	n := night(1, 5, 30*time.Minute)
	if got := NightIntensity(n, at(3, 0, 0)); !near(got, 1) {
		t.Errorf("= %v", got)
	}
	if got := NightIntensity(n, at(12, 0, 0)); got != 0 {
		t.Errorf("= %v", got)
	}
}

func TestNightGains(t *testing.T) {
	n := night(21, 7, 30*time.Minute)
	tests := []struct {
		i       float64
		r, g, b int
	}{
		{0, 100, 100, 100},
		{0.5, 100, 100, 80},
		{1, 100, 100, 60},
		{2, 100, 100, 60},   // saturates
		{-1, 100, 100, 100}, // saturates
	}
	for _, tc := range tests {
		r, g, b := NightGains(n, tc.i)
		if r != tc.r || g != tc.g || b != tc.b {
			t.Errorf("i=%v: %d,%d,%d; want %d,%d,%d", tc.i, r, g, b, tc.r, tc.g, tc.b)
		}
	}
}

func TestStep(t *testing.T) {
	tests := []struct{ cur, tgt, max, want int }{
		{40, 20, 1, 39},
		{20, 40, 1, 21},
		{30, 30, 1, 30},
		{30, 31, 1, 31},
		{30, 80, 5, 35},
		{30, 28, 5, 28},
		{30, 80, 0, 31}, // invalid max becomes 1
	}
	for _, tc := range tests {
		if got := Step(tc.cur, tc.tgt, tc.max); got != tc.want {
			t.Errorf("Step(%d,%d,%d) = %d; want %d", tc.cur, tc.tgt, tc.max, got, tc.want)
		}
	}
}

func TestStepRampWritesAtMostTwentyTimes(t *testing.T) {
	cur, writes := 40, 0
	for i := 0; i < 100; i++ {
		next := Step(cur, 20, 1)
		if next != cur {
			if d := next - cur; d < -1 || d > 1 {
				t.Fatalf("step larger than 1: %d", d)
			}
			writes++
			cur = next
		}
	}
	if cur != 20 || writes != 20 {
		t.Errorf("cur=%d writes=%d; want 20 and 20", cur, writes)
	}
}

func TestNightLightKelvin(t *testing.T) {
	tests := []struct {
		i    float64
		peak int
		want int
	}{
		{0, 2700, 4700},
		{1, 2700, 2700},
		{0.5, 2700, 3700},
		{2, 2700, 2700},  // saturates
		{-1, 2700, 4700}, // saturates
		{1, 1000, 1700},  // peak below the GNOME minimum
		{1, 9000, 4700},  // peak above neutral
	}
	for _, tc := range tests {
		if got := NightLightKelvin(tc.i, tc.peak); got != tc.want {
			t.Errorf("NightLightKelvin(%v,%d) = %d; want %d", tc.i, tc.peak, got, tc.want)
		}
	}
}

func TestNightPhase(t *testing.T) {
	n := night(21, 7, 30*time.Minute)
	tests := []struct {
		name string
		at   time.Time
		want Phase
		next time.Time
	}{
		{"afternoon", at(14, 0, 0), PhaseDay, at(21, 0, 0)},
		{"just before start", at(20, 59, 0), PhaseDay, at(21, 0, 0)},
		{"start", at(21, 0, 0), PhaseFadingIn, at(21, 30, 0)},
		{"middle of the fade in", at(21, 15, 0), PhaseFadingIn, at(21, 30, 0)},
		{"plateau", at(23, 0, 0), PhaseNight, time.Date(2026, 11, 14, 7, 0, 0, 0, time.UTC)},
		{"after midnight", at(3, 0, 0), PhaseNight, at(7, 0, 0)},
		{"end", at(7, 0, 0), PhaseNight, at(7, 0, 0)},
		{"fading out", at(7, 10, 0), PhaseFadingOut, at(7, 30, 0)},
		{"morning", at(7, 30, 0), PhaseDay, at(21, 0, 0)},
	}
	for _, tc := range tests {
		got, next := NightPhase(n, tc.at)
		if got != tc.want || !next.Equal(tc.next) {
			t.Errorf("%s: %s until %v; want %s until %v", tc.name, got, next, tc.want, tc.next)
		}
	}
	off := n
	off.Enabled = false
	if p, next := NightPhase(off, at(3, 0, 0)); p != PhaseOff || !next.IsZero() {
		t.Errorf("disabled: %s %v", p, next)
	}
}
