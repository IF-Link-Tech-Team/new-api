package ratio_setting

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// Peak/off-peak pricing window in server-local time, format "HH:MM-HH:MM"
// (start inclusive, end exclusive). A window that wraps past midnight is
// allowed (e.g. "23:00-09:00" means peak from 23:00 through 09:00 the next
// day).
//
// An empty or unparsable window disables the off-peak logic entirely:
// IsOffPeakTime always returns false and every model bills at its base
// ModelRatio at all hours. This is the safe default — an operator must
// explicitly configure a window before any off-peak discount applies, so a
// bad value can never under-price a request during peak upstream cost.

var now = time.Now // injectable clock for tests

type peakWindow struct {
	configured bool
	raw        string
	startMin   int // minutes since midnight, inclusive
	endMin     int // minutes since midnight, exclusive
}

var (
	peakWindowMu     sync.RWMutex
	activePeakWindow peakWindow
)

func parseHM(s string) (int, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// SetPeakTimeWindow accepts "HH:MM-HH:MM" in server-local time. Any invalid
// value disables the off-peak logic (IsOffPeakTime becomes always false).
func SetPeakTimeWindow(raw string) {
	peakWindowMu.Lock()
	defer peakWindowMu.Unlock()
	activePeakWindow = peakWindow{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	bounds := strings.Split(raw, "-")
	if len(bounds) != 2 {
		return
	}
	start, ok1 := parseHM(bounds[0])
	end, ok2 := parseHM(bounds[1])
	if !ok1 || !ok2 || start == end {
		return
	}
	activePeakWindow = peakWindow{
		configured: true,
		raw:        raw,
		startMin:   start,
		endMin:     end,
	}
	InvalidateExposedDataCache()
}

// IsOffPeakTime reports whether the current server-local time is outside the
// configured peak window. Returns false when no window is configured.
func IsOffPeakTime() bool {
	peakWindowMu.RLock()
	w := activePeakWindow
	peakWindowMu.RUnlock()
	if !w.configured {
		return false
	}
	t := now()
	m := t.Hour()*60 + t.Minute()
	var inPeak bool
	if w.startMin < w.endMin {
		// plain window: peak = [start, end)
		inPeak = m >= w.startMin && m < w.endMin
	} else {
		// wraps midnight: peak = [start, 24:00) ∪ [00:00, end)
		inPeak = m >= w.startMin || m < w.endMin
	}
	return !inPeak
}

// PeakTimeWindowJSONString returns the configured window string ("" when
// unset) for UI / exposed-data display.
func PeakTimeWindowJSONString() string {
	peakWindowMu.RLock()
	defer peakWindowMu.RUnlock()
	return activePeakWindow.raw
}
