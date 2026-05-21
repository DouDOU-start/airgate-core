package account

import (
	"testing"
	"time"
)

func TestNormalizeAccountUsageWindowContractFields(t *testing.T) {
	cases := []struct {
		name        string
		input       AccountUsageWindow
		wantSlot    string
		wantGroup   string
		wantDisplay string
	}{
		{
			name:        "openai model window",
			input:       AccountUsageWindow{Key: "model:5h:gpt-5.3-codex-spark", Label: "5h gpt-5.3-codex-spark"},
			wantSlot:    "5h",
			wantGroup:   "model:gpt-5.3-codex-spark",
			wantDisplay: "5h",
		},
		{
			name:        "claude sonnet window",
			input:       AccountUsageWindow{Key: "7d_sonnet", Label: "7d Sonnet"},
			wantSlot:    "7d",
			wantGroup:   "model:sonnet",
			wantDisplay: "7d",
		},
		{
			name:        "kiro monthly credits window",
			input:       AccountUsageWindow{Key: "monthly", Label: "Cr 12/100", ResetAfterSeconds: 60},
			wantSlot:    "monthly",
			wantGroup:   "base",
			wantDisplay: "Cr",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := normalizeAccountUsageWindow(tc.input)
			if !ok {
				t.Fatalf("normalizeAccountUsageWindow rejected %+v", tc.input)
			}
			if got.Slot != tc.wantSlot || got.Group != tc.wantGroup || got.DisplayLabel != tc.wantDisplay {
				t.Fatalf("normalized = %+v, want slot=%q group=%q display=%q", got, tc.wantSlot, tc.wantGroup, tc.wantDisplay)
			}
			if tc.input.ResetAfterSeconds > 0 && got.ResetSeconds != tc.input.ResetAfterSeconds {
				t.Fatalf("ResetSeconds = %d, want %d", got.ResetSeconds, tc.input.ResetAfterSeconds)
			}
		})
	}
}

func TestUsageCacheExpiresAtUsesEarlierResetOrFiveHours(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

	accounts := map[string]AccountUsageInfo{
		"1": {
			Windows: []AccountUsageWindow{
				{Key: "5h", ResetSeconds: int64((3 * time.Hour).Seconds())},
				{Key: "7d", ResetSeconds: int64((7 * 24 * time.Hour).Seconds())},
			},
		},
	}
	if got, want := usageCacheExpiresAt(accounts, now), now.Add(3*time.Hour); !got.Equal(want) {
		t.Fatalf("expiresAt = %s, want %s", got, want)
	}

	accounts["1"] = AccountUsageInfo{
		Windows: []AccountUsageWindow{
			{Key: "7d", ResetSeconds: int64((7 * 24 * time.Hour).Seconds())},
		},
	}
	if got, want := usageCacheExpiresAt(accounts, now), now.Add(usageCacheMaxTTL); !got.Equal(want) {
		t.Fatalf("expiresAt = %s, want %s", got, want)
	}
}

func TestMergeAccountUsageInfoPreservesLiveMissingWindows(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	existing := AccountUsageInfo{
		UpdatedAt: "2026-05-20T11:55:00Z",
		Windows: []AccountUsageWindow{
			{
				Key:          "5h",
				Label:        "5h",
				DisplayLabel: "5h",
				Slot:         "5h",
				Group:        "base",
				UsedPercent:  31,
				ResetAt:      now.Add(2 * time.Hour).Format(time.RFC3339),
			},
			{
				Key:          "7d",
				Label:        "7d",
				DisplayLabel: "7d",
				Slot:         "7d",
				Group:        "base",
				UsedPercent:  44,
				ResetAt:      now.Add(48 * time.Hour).Format(time.RFC3339),
			},
		},
	}
	incoming := AccountUsageInfo{
		UpdatedAt: "2026-05-20T12:00:00Z",
		Windows: []AccountUsageWindow{
			{
				Key:          "7d",
				Label:        "7d",
				DisplayLabel: "7d",
				Slot:         "7d",
				Group:        "base",
				UsedPercent:  55,
			},
		},
	}

	merged := mergeAccountUsageInfo(existing, incoming, now)
	if len(merged.Windows) != 2 {
		t.Fatalf("len(windows) = %d, want 2: %+v", len(merged.Windows), merged.Windows)
	}
	if got := merged.Windows[0]; got.Key != "5h" || got.UsedPercent != 31 || got.ResetSeconds <= 0 {
		t.Fatalf("preserved 5h window = %+v, want live cached 5h sorted first", got)
	}
	if got := merged.Windows[1]; got.Key != "7d" || got.UsedPercent != 55 || got.ResetSeconds <= 0 {
		t.Fatalf("merged 7d window = %+v, want incoming usage with preserved reset", got)
	}
}

func TestMergeAccountUsageInfoDropsExpiredMissingWindows(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	existing := AccountUsageInfo{
		Windows: []AccountUsageWindow{
			{
				Key:         "5h",
				Label:       "5h",
				UsedPercent: 31,
				ResetAt:     now.Add(-time.Minute).Format(time.RFC3339),
			},
		},
	}
	incoming := AccountUsageInfo{Windows: []AccountUsageWindow{{Key: "7d", Label: "7d", UsedPercent: 55}}}

	merged := mergeAccountUsageInfo(existing, incoming, now)
	if len(merged.Windows) != 1 || merged.Windows[0].Key != "7d" {
		t.Fatalf("windows = %+v, want only incoming 7d", merged.Windows)
	}
}

func TestMergeAccountUsageInfoStableSortByDuration(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	existing := AccountUsageInfo{}
	incoming := AccountUsageInfo{
		Windows: []AccountUsageWindow{
			{Key: "monthly", Slot: "monthly", Label: "monthly", UsedPercent: 11, ResetAt: now.Add(15 * 24 * time.Hour).Format(time.RFC3339)},
			{Key: "7d", Slot: "7d", Label: "7d", UsedPercent: 22, ResetAt: now.Add(48 * time.Hour).Format(time.RFC3339)},
			{Key: "5h", Slot: "5h", Label: "5h", UsedPercent: 33, ResetAt: now.Add(2 * time.Hour).Format(time.RFC3339)},
		},
	}

	merged := mergeAccountUsageInfo(existing, incoming, now)
	got := []string{merged.Windows[0].Slot, merged.Windows[1].Slot, merged.Windows[2].Slot}
	want := []string{"5h", "7d", "monthly"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("window[%d] slot = %q, want %q (full order %v)", i, got[i], want[i], got)
		}
	}
}

func TestMergeAccountUsageWindowCarriesMissingFieldsAndReset(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	existing := AccountUsageWindow{
		Key:          "5h",
		Label:        "5h",
		DisplayLabel: "5h window",
		Slot:         "5h",
		Group:        "base",
		UpdatedAt:    "2026-05-20T11:55:00Z",
		UsedPercent:  10,
		ResetAt:      now.Add(2 * time.Hour).Format(time.RFC3339),
	}
	incoming := AccountUsageWindow{Key: "5h", UsedPercent: 42}

	merged := mergeAccountUsageWindow(existing, incoming, now)
	if merged.Label != "5h" || merged.DisplayLabel != "5h window" || merged.Slot != "5h" || merged.Group != "base" {
		t.Fatalf("label-family not carried: %+v", merged)
	}
	if merged.UpdatedAt != existing.UpdatedAt {
		t.Fatalf("UpdatedAt = %q, want carry-over %q", merged.UpdatedAt, existing.UpdatedAt)
	}
	if merged.ResetSeconds <= 0 || merged.ResetAfterSeconds <= 0 || merged.ResetAt == "" {
		t.Fatalf("expected reset fields to be filled from existing window: %+v", merged)
	}
}

func TestMergeAccountUsageWindowKeepsIncomingResetWhenProvided(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	existing := AccountUsageWindow{Key: "5h", ResetAt: now.Add(10 * time.Hour).Format(time.RFC3339)}
	incoming := AccountUsageWindow{Key: "5h", ResetAfterSeconds: 60}

	merged := mergeAccountUsageWindow(existing, incoming, now)
	if merged.ResetAfterSeconds != 60 || merged.ResetAt != "" {
		t.Fatalf("expected incoming reset to win: %+v", merged)
	}
}

func TestAccountUsageWindowIdentityFallbacks(t *testing.T) {
	cases := []struct {
		name   string
		window AccountUsageWindow
		want   string
	}{
		{"key wins", AccountUsageWindow{Key: " primary ", Group: "base", Slot: "5h", Label: "5h"}, "primary"},
		{"group+slot+display", AccountUsageWindow{Group: "base", Slot: "5h", DisplayLabel: "5h window"}, "base:5h:5h window"},
		{"group+slot+label fallback", AccountUsageWindow{Group: "model:opus", Slot: "7d", Label: "7d Opus"}, "model:opus:7d:7d Opus"},
		{"label-only", AccountUsageWindow{Label: " 5h "}, "5h"},
		{"empty", AccountUsageWindow{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := accountUsageWindowIdentity(tc.window); got != tc.want {
				t.Fatalf("identity = %q, want %q", got, tc.want)
			}
		})
	}
}
