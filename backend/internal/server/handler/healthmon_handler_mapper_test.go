package handler

import (
	"testing"

	apphealthmon "github.com/DouDOU-start/airgate-core/internal/app/healthmon"
)

func TestToHealthmonLatencyIncludesRealP95(t *testing.T) {
	got := toHealthmonLatency(apphealthmon.Latency{AvgMs: 1_200, P95Ms: 2_400, MaxMs: 82_000})
	if got.AvgMs != 1_200 || got.P95Ms != 2_400 || got.MaxMs != 82_000 {
		t.Fatalf("latency DTO = %+v, want avg=1200 p95=2400 max=82000", got)
	}
}
