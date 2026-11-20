package transcript

import (
	"testing"
	"time"
)

const gatewayQuota = "Request rejected (429) · upstream account quota usage limit has been reached; an account becomes available again at "

func TestGatewayUsageLimitReset(t *testing.T) {
	at := time.Date(2026, 11, 19, 5, 55, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, text string
		at, want   time.Time
		ok         bool
	}{
		{"supplied quota", gatewayQuota + "18:02 UTC (in 12h7m)", at, at.Add(12*time.Hour + 7*time.Minute), true},
		{"next day", gatewayQuota + "04:00 UTC (in 22h5m)", at, time.Date(2026, 11, 20, 4, 0, 0, 0, time.UTC), true},
		{"dated", gatewayQuota + "2026-11-21 04:00 UTC (in 2d)", at, time.Date(2026, 11, 21, 4, 0, 0, 0, time.UTC), true},
		{"missing timestamp", gatewayQuota + "18:02 UTC", time.Time{}, time.Time{}, false},
		{"invalid clock", gatewayQuota + "25:02 UTC", at, time.Time{}, false},
		{"invalid date", gatewayQuota + "2026-02-30 18:02 UTC", at, time.Time{}, false},
		{"missing zone", gatewayQuota + "18:02", at, time.Time{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reset, ok := UsageLimitReset(tc.text, tc.at)
			if ok != tc.ok || !reset.Equal(tc.want) {
				t.Fatalf("reset = %v, %v; want %v, %v", reset, ok, tc.want, tc.ok)
			}
		})
	}
	for _, text := range []string{gatewayQuota + "18:02 UTC", "API Error: " + gatewayQuota + "18:02 UTC"} {
		if !UsageLimitText(text) {
			t.Fatalf("quota not recognized: %q", text)
		}
		if UsageLimitText("The log said: " + text) {
			t.Fatal("a quoted error became a usage limit")
		}
	}
}
