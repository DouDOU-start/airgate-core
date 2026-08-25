package handler

import (
	"context"
	"strconv"
	"strings"

	appsettings "github.com/DouDOU-start/airgate-core/internal/app/settings"
)

const fallbackDefaultUserMaxConcurrency = 5

func defaultUserMaxConcurrency(ctx context.Context, settingsService *appsettings.Service) int {
	if settingsService == nil {
		return fallbackDefaultUserMaxConcurrency
	}
	settings, err := settingsService.List(ctx, "defaults")
	if err != nil {
		return fallbackDefaultUserMaxConcurrency
	}
	for _, setting := range settings {
		if setting.Key != "default_concurrency" {
			continue
		}
		if value, err := strconv.Atoi(strings.TrimSpace(setting.Value)); err == nil && value > 0 {
			return value
		}
	}
	return fallbackDefaultUserMaxConcurrency
}
