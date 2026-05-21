package account

import (
	"sort"
	"strings"
	"time"
)

const accountUsageCacheVersion = 2

type AccountUsageWindow struct {
	Key               string  `json:"key,omitempty"`
	Label             string  `json:"label,omitempty"`
	DisplayLabel      string  `json:"display_label,omitempty"`
	Slot              string  `json:"slot,omitempty"`
	Group             string  `json:"group,omitempty"`
	UsedPercent       float64 `json:"used_percent"`
	ResetAt           string  `json:"reset_at,omitempty"`
	ResetSeconds      int64   `json:"reset_seconds,omitempty"`
	ResetAfterSeconds int64   `json:"reset_after_seconds,omitempty"`
	UpdatedAt         string  `json:"updated_at,omitempty"`
	IgnoreLimit       bool    `json:"ignore_limit,omitempty"`
	EnforceLimit      *bool   `json:"enforce_limit,omitempty"`
	SortOrder         int     `json:"sort_order,omitempty"`
}

type AccountUsageCredits struct {
	Balance   float64 `json:"balance"`
	Unlimited bool    `json:"unlimited"`
}

type AccountUsageInfo struct {
	UpdatedAt string               `json:"updated_at,omitempty"`
	Windows   []AccountUsageWindow `json:"windows,omitempty"`
	Credits   *AccountUsageCredits `json:"credits,omitempty"`
}

type accountUsageError struct {
	ID      int    `json:"id"`
	Message string `json:"message"`
}

type accountUsagePluginResponse struct {
	Accounts map[string]AccountUsageInfo `json:"accounts"`
	Errors   []accountUsageError         `json:"errors,omitempty"`
}

type accountUsageCachePayload struct {
	Version   int                         `json:"version"`
	FetchedAt string                      `json:"fetched_at"`
	ExpiresAt string                      `json:"expires_at,omitempty"`
	Accounts  map[string]AccountUsageInfo `json:"accounts"`
}

func newAccountUsageCachePayload(accounts map[string]AccountUsageInfo, now, expiresAt time.Time) accountUsageCachePayload {
	return accountUsageCachePayload{
		Version:   accountUsageCacheVersion,
		FetchedAt: now.UTC().Format(time.RFC3339),
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
		Accounts:  accounts,
	}
}

func (p accountUsageCachePayload) valid() bool {
	return p.Version == accountUsageCacheVersion && p.Accounts != nil
}

func (p accountUsageCachePayload) cacheExpiresAt(now time.Time) time.Time {
	if p.ExpiresAt != "" {
		if parsed, err := time.Parse(time.RFC3339, p.ExpiresAt); err == nil {
			return parsed
		}
	}
	return usageCacheExpiresAt(p.Accounts, now)
}

func usageCacheExpiresAt(accounts map[string]AccountUsageInfo, now time.Time) time.Time {
	expiresAt := now.Add(usageCacheMaxTTL)
	for _, account := range accounts {
		for _, window := range account.Windows {
			resetAt, ok := accountUsageWindowResetAt(window, now)
			if !ok {
				continue
			}
			if !resetAt.After(now) {
				return now
			}
			if resetAt.Before(expiresAt) {
				expiresAt = resetAt
			}
		}
	}
	return expiresAt
}

func accountUsageWindowResetAt(window AccountUsageWindow, now time.Time) (time.Time, bool) {
	if window.ResetAt != "" {
		parsed, err := time.Parse(time.RFC3339, window.ResetAt)
		if err == nil {
			return parsed, true
		}
	}
	if window.ResetSeconds > 0 {
		return now.Add(time.Duration(window.ResetSeconds) * time.Second), true
	}
	if window.ResetAfterSeconds > 0 {
		return now.Add(time.Duration(window.ResetAfterSeconds) * time.Second), true
	}
	return time.Time{}, false
}

func normalizeAccountUsageInfo(info AccountUsageInfo) AccountUsageInfo {
	info.UpdatedAt = strings.TrimSpace(info.UpdatedAt)
	if len(info.Windows) == 0 {
		return info
	}
	windows := make([]AccountUsageWindow, 0, len(info.Windows))
	for _, window := range info.Windows {
		normalized, ok := normalizeAccountUsageWindow(window)
		if ok {
			windows = append(windows, normalized)
		}
	}
	info.Windows = windows
	return info
}

func normalizeAccountUsageWindow(window AccountUsageWindow) (AccountUsageWindow, bool) {
	window.Key = strings.TrimSpace(window.Key)
	window.Label = strings.TrimSpace(window.Label)
	window.DisplayLabel = strings.TrimSpace(window.DisplayLabel)
	window.Slot = normalizeUsageWindowToken(window.Slot)
	window.Group = strings.TrimSpace(window.Group)
	window.UpdatedAt = strings.TrimSpace(window.UpdatedAt)
	window.ResetAt = strings.TrimSpace(window.ResetAt)
	if window.ResetSeconds <= 0 && window.ResetAfterSeconds > 0 {
		window.ResetSeconds = window.ResetAfterSeconds
	}
	if window.Slot == "" {
		window.Slot = inferUsageWindowSlot(window.Key, window.Label)
	}
	if window.Group == "" {
		window.Group = inferUsageWindowGroup(window.Key, window.Label, window.Slot)
	}
	if window.DisplayLabel == "" {
		window.DisplayLabel = inferUsageWindowDisplayLabel(window.Key, window.Label, window.Slot)
	}
	if window.Label == "" {
		window.Label = window.DisplayLabel
	}
	if window.Key == "" {
		window.Key = inferUsageWindowKey(window.Group, window.Slot, window.Label)
	}
	if window.Label == "" && window.Key == "" {
		return AccountUsageWindow{}, false
	}
	return window, true
}

func mergeAccountUsageInfo(existing, incoming AccountUsageInfo, now time.Time) AccountUsageInfo {
	merged := incoming
	if merged.UpdatedAt == "" {
		merged.UpdatedAt = existing.UpdatedAt
	}
	if merged.Credits == nil {
		merged.Credits = existing.Credits
	}
	if len(existing.Windows) == 0 {
		sortAccountUsageWindows(merged.Windows)
		return merged
	}
	if len(merged.Windows) == 0 {
		merged.Windows = liveAccountUsageWindows(existing.Windows, now)
		return merged
	}

	existingByID := make(map[string]AccountUsageWindow, len(existing.Windows))
	for _, window := range existing.Windows {
		id := accountUsageWindowIdentity(window)
		if id == "" {
			continue
		}
		existingByID[id] = window
	}

	windows := make([]AccountUsageWindow, 0, len(merged.Windows)+len(existingByID))
	seen := make(map[string]struct{}, len(merged.Windows))
	for _, window := range merged.Windows {
		id := accountUsageWindowIdentity(window)
		if id != "" {
			if cached, ok := existingByID[id]; ok {
				window = mergeAccountUsageWindow(cached, window, now)
			}
			seen[id] = struct{}{}
		}
		windows = append(windows, window)
	}
	for _, window := range existing.Windows {
		id := accountUsageWindowIdentity(window)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		resetAt, ok := accountUsageWindowResetAt(window, now)
		if !ok || !resetAt.After(now) {
			continue
		}
		windows = append(windows, windowWithResetAt(window, resetAt, now))
	}
	sortAccountUsageWindows(windows)
	merged.Windows = windows
	return merged
}

// 让窗口在合并后顺序不依赖上游本次返回的顺序，避免 UI 在两次刷新之间出现 5h/7d 调换的视觉抖动。
func sortAccountUsageWindows(windows []AccountUsageWindow) {
	sort.SliceStable(windows, func(i, j int) bool {
		si, sj := slotSortRank(windows[i].Slot), slotSortRank(windows[j].Slot)
		if si != sj {
			return si < sj
		}
		if windows[i].Group != windows[j].Group {
			return windows[i].Group < windows[j].Group
		}
		if windows[i].Key != windows[j].Key {
			return windows[i].Key < windows[j].Key
		}
		return windows[i].Label < windows[j].Label
	})
}

func slotSortRank(slot string) int {
	switch normalizeUsageWindowToken(slot) {
	case "5h":
		return 0
	case "7d":
		return 1
	case "monthly":
		return 2
	default:
		return 3
	}
}

func liveAccountUsageWindows(windows []AccountUsageWindow, now time.Time) []AccountUsageWindow {
	result := make([]AccountUsageWindow, 0, len(windows))
	for _, window := range windows {
		resetAt, ok := accountUsageWindowResetAt(window, now)
		if !ok || !resetAt.After(now) {
			continue
		}
		result = append(result, windowWithResetAt(window, resetAt, now))
	}
	sortAccountUsageWindows(result)
	return result
}

func mergeAccountUsageWindow(existing, incoming AccountUsageWindow, now time.Time) AccountUsageWindow {
	merged := incoming
	if merged.Label == "" {
		merged.Label = existing.Label
	}
	if merged.DisplayLabel == "" {
		merged.DisplayLabel = existing.DisplayLabel
	}
	if merged.Slot == "" {
		merged.Slot = existing.Slot
	}
	if merged.Group == "" {
		merged.Group = existing.Group
	}
	if merged.UpdatedAt == "" {
		merged.UpdatedAt = existing.UpdatedAt
	}
	if merged.ResetAt == "" && merged.ResetSeconds <= 0 && merged.ResetAfterSeconds <= 0 {
		if resetAt, ok := accountUsageWindowResetAt(existing, now); ok && resetAt.After(now) {
			merged = windowWithResetAt(merged, resetAt, now)
		}
	}
	return merged
}

func windowWithResetAt(window AccountUsageWindow, resetAt, now time.Time) AccountUsageWindow {
	window.ResetAt = resetAt.UTC().Format(time.RFC3339)
	remaining := resetAt.Sub(now)
	if remaining > 0 {
		window.ResetSeconds = int64(remaining.Seconds())
		window.ResetAfterSeconds = window.ResetSeconds
	}
	return window
}

func accountUsageWindowIdentity(window AccountUsageWindow) string {
	if key := strings.TrimSpace(window.Key); key != "" {
		return key
	}
	group := strings.TrimSpace(window.Group)
	slot := normalizeUsageWindowToken(window.Slot)
	if group != "" || slot != "" {
		label := strings.TrimSpace(window.DisplayLabel)
		if label == "" {
			label = strings.TrimSpace(window.Label)
		}
		return group + ":" + slot + ":" + label
	}
	return strings.TrimSpace(window.Label)
}

func inferUsageWindowDisplayLabel(key, label, slot string) string {
	if slot == "monthly" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(label)), "cr ") {
		return "Cr"
	}
	if slot != "" {
		return slot
	}
	return strings.TrimSpace(key)
}

func normalizeUsageWindowToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

func inferUsageWindowSlot(key, label string) string {
	keyLower := normalizeUsageWindowToken(key)
	labelLower := normalizeUsageWindowToken(label)
	switch {
	case keyLower == "5h" || strings.Contains(keyLower, ":5h") || strings.HasPrefix(labelLower, "5h"):
		return "5h"
	case keyLower == "7d" || keyLower == "7d-sonnet" || strings.Contains(keyLower, ":7d") || strings.HasPrefix(labelLower, "7d"):
		return "7d"
	case keyLower == "monthly" || strings.Contains(keyLower, "monthly") || strings.Contains(labelLower, "monthly"):
		return "monthly"
	case keyLower != "":
		return keyLower
	case labelLower != "":
		return strings.Fields(labelLower)[0]
	default:
		return ""
	}
}

func inferUsageWindowGroup(key, label, slot string) string {
	key = strings.TrimSpace(key)
	if strings.HasPrefix(key, "model:") {
		return strings.TrimSpace(strings.TrimPrefix(strings.Replace(key, "model:"+slot+":", "model:", 1), "model::"))
	}
	if suffix := usageWindowLabelSuffix(label, slot); suffix != "" {
		return "model:" + usageWindowGroupSlug(suffix)
	}
	if strings.HasPrefix(key, "7d_") || strings.HasPrefix(key, "5h_") {
		parts := strings.SplitN(key, "_", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			return "model:" + usageWindowGroupSlug(parts[1])
		}
	}
	return "base"
}

func usageWindowLabelSuffix(label, slot string) string {
	label = strings.TrimSpace(label)
	if label == "" || slot == "" {
		return ""
	}
	fields := strings.Fields(label)
	if len(fields) <= 1 || !strings.EqualFold(fields[0], slot) {
		return ""
	}
	return strings.TrimSpace(strings.Join(fields[1:], " "))
}

func usageWindowGroupSlug(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	return value
}

func inferUsageWindowKey(group, slot, label string) string {
	if slot == "" {
		return strings.TrimSpace(label)
	}
	if group == "" || group == "base" {
		return slot
	}
	return group + ":" + slot
}

func accountUsageInfosToMap(accounts map[string]AccountUsageInfo) map[string]any {
	out := make(map[string]any, len(accounts))
	for id, info := range accounts {
		out[id] = accountUsageInfoToMap(info)
	}
	return out
}

func accountUsageInfoToMap(info AccountUsageInfo) map[string]any {
	out := make(map[string]any, 3)
	if info.UpdatedAt != "" {
		out["updated_at"] = info.UpdatedAt
	}
	if len(info.Windows) > 0 {
		windows := make([]any, 0, len(info.Windows))
		for _, window := range info.Windows {
			windows = append(windows, accountUsageWindowToMap(window))
		}
		out["windows"] = windows
	}
	if info.Credits != nil {
		out["credits"] = map[string]any{
			"balance":   info.Credits.Balance,
			"unlimited": info.Credits.Unlimited,
		}
	}
	return out
}

func accountUsageWindowToMap(window AccountUsageWindow) map[string]any {
	out := make(map[string]any, 12)
	if window.Key != "" {
		out["key"] = window.Key
	}
	if window.Label != "" {
		out["label"] = window.Label
	}
	if window.DisplayLabel != "" {
		out["display_label"] = window.DisplayLabel
	}
	if window.Slot != "" {
		out["slot"] = window.Slot
	}
	if window.Group != "" {
		out["group"] = window.Group
	}
	out["used_percent"] = window.UsedPercent
	if window.ResetAt != "" {
		out["reset_at"] = window.ResetAt
	}
	if window.ResetSeconds > 0 {
		out["reset_seconds"] = window.ResetSeconds
	}
	if window.ResetAfterSeconds > 0 {
		out["reset_after_seconds"] = window.ResetAfterSeconds
	}
	if window.UpdatedAt != "" {
		out["updated_at"] = window.UpdatedAt
	}
	if window.IgnoreLimit {
		out["ignore_limit"] = true
	}
	if window.EnforceLimit != nil {
		out["enforce_limit"] = *window.EnforceLimit
	}
	if window.SortOrder != 0 {
		out["sort_order"] = window.SortOrder
	}
	return out
}
