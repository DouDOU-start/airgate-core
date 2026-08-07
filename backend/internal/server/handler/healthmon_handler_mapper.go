package handler

import (
	"time"

	apphealthmon "github.com/DouDOU-start/airgate-core/internal/app/healthmon"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toHealthmonOverviewResp(item apphealthmon.Overview) dto.HealthmonOverviewResp {
	return dto.HealthmonOverviewResp{
		Window:      string(item.Window),
		Sample:      toHealthmonSample(item.Sample),
		SuccessRate: item.SuccessRate,
		ErrorRate:   item.ErrorRate,
		Counts:      toHealthmonCounts(item.Counts),
		Latency:     toHealthmonLatency(item.Latency),
		TTFT:        toHealthmonLatency(item.TTFT),
		HealthScore: item.HealthScore,
		Availability: dto.HealthmonAvailability{
			ChannelKeysAvailable: item.Availability.ChannelKeysAvailable,
			ChannelKeysTotal:     item.Availability.ChannelKeysTotal,
		},
	}
}

func toHealthmonEntityResp(item apphealthmon.EntityRow) dto.HealthmonEntityResp {
	resp := dto.HealthmonEntityResp{
		ID:           item.ID,
		Kind:         string(item.Kind),
		Name:         item.Name,
		ChannelID:    item.ChannelID,
		ChannelName:  item.ChannelName,
		Platform:     item.Platform,
		Type:         item.Type,
		SchedStatus:  item.SchedStatus,
		HealthStatus: item.HealthStatus,
		Window:       string(item.Window),
		Sample:       toHealthmonSample(item.Sample),
		SuccessRate:  item.SuccessRate,
		ErrorRate:    item.ErrorRate,
		Counts:       toHealthmonCounts(item.Counts),
		Latency:      toHealthmonLatency(item.Latency),
		TTFT:         toHealthmonLatency(item.TTFT),
		HealthScore:  item.HealthScore,
	}
	if item.LastError != nil {
		resp.LastError = &dto.HealthmonLastError{
			At:      item.LastError.At.UTC().Format(time.RFC3339),
			Phase:   item.LastError.Phase,
			Message: item.LastError.Message,
		}
	}
	return resp
}

func toHealthmonSample(s apphealthmon.Sample) dto.HealthmonSample {
	return dto.HealthmonSample{
		N:         s.N,
		S:         s.S,
		E:         s.E,
		Idle:      s.Idle,
		LowSample: s.LowSample,
	}
}

func toHealthmonCounts(c apphealthmon.Counts) dto.HealthmonCounts {
	return dto.HealthmonCounts{
		Auth:        c.Auth,
		RateLimit:   c.RateLimit,
		Upstream5xx: c.Upstream5xx,
		Client:      c.Client,
		Canceled:    c.Canceled,
		Precheck:    c.Precheck,
		Other:       c.Other,
	}
}

func toHealthmonLatency(l apphealthmon.Latency) dto.HealthmonLatency {
	return dto.HealthmonLatency{AvgMs: l.AvgMs, MaxMs: l.MaxMs}
}

func toChannelStatusOverviewResp(item apphealthmon.UserOverview) dto.ChannelStatusOverviewResp {
	return dto.ChannelStatusOverviewResp{
		Window:      string(item.Window),
		Sample:      toChannelStatusSample(item.Sample),
		SuccessRate: item.SuccessRate,
		ErrorRate:   item.ErrorRate,
		Latency:     dto.ChannelStatusLatency{AvgMs: item.Latency.AvgMs},
		TTFT:        dto.ChannelStatusLatency{AvgMs: item.TTFT.AvgMs},
		HealthScore: item.HealthScore,
		UpdatedAt:   item.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toChannelStatusSample(item apphealthmon.Sample) dto.ChannelStatusSample {
	return dto.ChannelStatusSample{Idle: item.Idle, LowSample: item.LowSample}
}

func toChannelStatusGroupResp(item apphealthmon.UserGroupStatus) dto.ChannelStatusGroupResp {
	return dto.ChannelStatusGroupResp{
		ID:          item.ID,
		Name:        item.Name,
		Platform:    item.Platform,
		Status:      string(item.Status),
		Window:      string(item.Window),
		Sample:      toChannelStatusSample(item.Sample),
		SuccessRate: item.SuccessRate,
		ErrorRate:   item.ErrorRate,
		Latency:     dto.ChannelStatusLatency{AvgMs: item.Latency.AvgMs},
		TTFT:        dto.ChannelStatusLatency{AvgMs: item.TTFT.AvgMs},
		HealthScore: item.HealthScore,
	}
}
