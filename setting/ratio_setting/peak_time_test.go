package ratio_setting

import (
	"testing"
	"time"
)

func setClock(t *testing.T, at time.Time) {
	t.Helper()
	old := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = old })
}

func at(h, m int) time.Time {
	return time.Date(2026, 8, 29, h, m, 0, 0, time.Local)
}

func TestSetPeakTimeWindow(t *testing.T) {
	SetPeakTimeWindow("09:00-23:00")
	if !activePeakWindow.configured || activePeakWindow.startMin != 540 || activePeakWindow.endMin != 1380 {
		t.Fatalf("plain window not parsed: %+v", activePeakWindow)
	}
	SetPeakTimeWindow("23:00-09:00") // wraps midnight
	if !activePeakWindow.configured || activePeakWindow.startMin != 1380 || activePeakWindow.endMin != 540 {
		t.Fatalf("wrapping window not parsed: %+v", activePeakWindow)
	}
	for _, bad := range []string{"", "   ", "09:00", "9:00-23:00", "09:00-23:00-extra", "25:00-23:00", "09:60-23:00", "09:00-09:00", "xx:yy-zz:ww"} {
		SetPeakTimeWindow(bad)
		if activePeakWindow.configured {
			t.Fatalf("invalid window %q must disable off-peak logic, got %+v", bad, activePeakWindow)
		}
	}
	SetPeakTimeWindow("") // reset
}

func TestIsOffPeakTimeBoundaries(t *testing.T) {
	SetPeakTimeWindow("09:00-23:00")
	t.Cleanup(func() { SetPeakTimeWindow("") })

	cases := []struct {
		name string
		h, m int
		want bool // off-peak?
	}{
		{"before start", 8, 59, true},
		{"start inclusive", 9, 0, false},
		{"mid peak", 12, 30, false},
		{"end exclusive", 22, 59, false},
		{"after end", 23, 0, true},
		{"deep night", 3, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setClock(t, at(c.h, c.m))
			if got := IsOffPeakTime(); got != c.want {
				t.Fatalf("IsOffPeakTime at %02d:%02d = %v, want %v", c.h, c.m, got, c.want)
			}
		})
	}
}

func TestIsOffPeakTimeWrapAround(t *testing.T) {
	SetPeakTimeWindow("23:00-09:00")
	t.Cleanup(func() { SetPeakTimeWindow("") })

	cases := []struct {
		name string
		h, m int
		want bool // off-peak?
	}{
		{"peak before midnight", 23, 30, false},
		{"midnight inside window", 0, 30, false},
		{"peak just before end", 8, 59, false},
		{"end exclusive", 9, 0, true},
		{"mid day", 14, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setClock(t, at(c.h, c.m))
			if got := IsOffPeakTime(); got != c.want {
				t.Fatalf("IsOffPeakTime at %02d:%02d = %v, want %v", c.h, c.m, got, c.want)
			}
		})
	}
}

func TestIsOffPeakTimeUnconfigured(t *testing.T) {
	SetPeakTimeWindow("")
	for _, c := range []int{0, 12, 23} {
		setClock(t, at(c, 0))
		if IsOffPeakTime() {
			t.Fatalf("IsOffPeakTime must be false when no window is configured (at %02d:00)", c)
		}
	}
}

func TestGetModelRatioOffPeakOverride(t *testing.T) {
	const model = "test-offpeak-model"
	prevMR, prevOff := modelRatioMap.ReadAll(), offPeakModelRatioMap.ReadAll()
	modelRatioMap.Clear()
	offPeakModelRatioMap.Clear()
	modelRatioMap.AddAll(map[string]float64{model: 1.0})
	offPeakModelRatioMap.AddAll(map[string]float64{model: 0.5})
	t.Cleanup(func() {
		modelRatioMap.Clear()
		offPeakModelRatioMap.Clear()
		modelRatioMap.AddAll(prevMR)
		offPeakModelRatioMap.AddAll(prevOff)
		SetPeakTimeWindow("")
	})

	SetPeakTimeWindow("09:00-23:00")

	setClock(t, at(10, 0)) // peak
	if r, ok, _ := GetModelRatio(model); !ok || r != 1.0 {
		t.Fatalf("peak ratio = %v (ok=%v), want 1.0", r, ok)
	}
	setClock(t, at(23, 30)) // off-peak
	if r, ok, _ := GetModelRatio(model); !ok || r != 0.5 {
		t.Fatalf("off-peak ratio = %v (ok=%v), want 0.5", r, ok)
	}
}

func TestGetModelRatioWithoutOffPeakEntry(t *testing.T) {
	const model = "test-no-offpeak"
	prevMR := modelRatioMap.ReadAll()
	modelRatioMap.Clear()
	modelRatioMap.AddAll(map[string]float64{model: 1.0})
	t.Cleanup(func() {
		modelRatioMap.Clear()
		modelRatioMap.AddAll(prevMR)
		SetPeakTimeWindow("")
	})

	SetPeakTimeWindow("09:00-23:00")
	setClock(t, at(23, 30)) // off-peak, but no off-peak entry
	if r, ok, _ := GetModelRatio(model); !ok || r != 1.0 {
		t.Fatalf("model without off-peak entry must keep base ratio, got %v (ok=%v)", r, ok)
	}
}

func TestPeakTimeWindowJSONString(t *testing.T) {
	SetPeakTimeWindow("23:00-09:00")
	if got := PeakTimeWindowJSONString(); got != "23:00-09:00" {
		t.Fatalf("PeakTimeWindowJSONString = %q, want %q", got, "23:00-09:00")
	}
	SetPeakTimeWindow("")
	if got := PeakTimeWindowJSONString(); got != "" {
		t.Fatalf("PeakTimeWindowJSONString after reset = %q, want empty", got)
	}
}
