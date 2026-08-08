package healthmon

import "testing"

func TestClassifyFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		phase  string
		code   string
		want   ErrorClass
		sla    bool
	}{
		{"401 auth", 401, "upstream_exhausted", "", ClassAuth, true},
		{"403 auth", 403, "", "", ClassAuth, true},
		{"upstream 402 auth", 402, "upstream_exhausted", "", ClassAuth, true},
		{"bare 402 not sla", 402, "", "", ClassClient, false},
		{"429 rate", 429, "upstream_exhausted", "", ClassRateLimit, true},
		{"500 upstream", 500, "", "", ClassUpstream5xx, true},
		{"503 upstream", 503, "upstream_exhausted", "", ClassUpstream5xx, true},
		{"408 timeout", 408, "", "", ClassUpstream5xx, true},
		{"400 client", 400, "upstream_client_error", "", ClassClient, false},
		{"phase precheck balance", 402, "precheck_balance", "insufficient_balance", ClassPrecheck, false},
		{"phase precheck price", 400, "precheck_price", "model_price_not_configured", ClassPrecheck, false},
		{"phase precheck rate", 403, "precheck_rate", "billing_rate_exceeded", ClassPrecheck, false},
		{"phase client restrict", 403, "precheck_client_restrict", "client_restricted", ClassPrecheck, false},
		{"phase moderation", 403, "precheck_moderation", "", ClassPrecheck, false},
		{"code balance without phase", 402, "", "insufficient_balance", ClassPrecheck, false},
		{"code price without phase", 400, "", "model_price_not_configured", ClassPrecheck, false},
		{"phase canceled", 499, "canceled", "", ClassCanceled, false},
		{"phase bad request", 400, "bad_request", "", ClassClient, false},
		{"phase stream aborted", 0, "stream_aborted", "", ClassUpstream5xx, true},
		{"network no status", 0, "upstream_exhausted", "", ClassUpstream5xx, true},
		{"local limit", 429, "local_limit", "", ClassPrecheck, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyFailure(tc.status, tc.phase, tc.code)
			if got != tc.want {
				t.Fatalf("ClassifyFailure(%d,%q,%q)=%s want %s", tc.status, tc.phase, tc.code, got, tc.want)
			}
			if CountsTowardErrorRate(got) != tc.sla {
				t.Fatalf("CountsTowardErrorRate(%s)=%v want %v", got, CountsTowardErrorRate(got), tc.sla)
			}
		})
	}
}

func TestComputeHealthScoreIdle(t *testing.T) {
	t.Parallel()
	sample := BuildSample(0, 0, DefaultMinSample)
	if !sample.Idle {
		t.Fatal("expected idle")
	}
	if score := ComputeHealthScore(sample, 0, 0, false); score != nil {
		t.Fatalf("idle score=%v want nil", score)
	}
}

func TestComputeHealthScoreRates(t *testing.T) {
	t.Parallel()
	sample := BuildSample(90, 10, DefaultMinSample) // 10% error
	score := ComputeHealthScore(sample, 0.10, 1000, true)
	if score == nil {
		t.Fatal("score nil")
	}
	// errorScore=0, ttft=100 → 0.6*0+0.4*100=40
	if *score != 40 {
		t.Fatalf("score=%d want 40", *score)
	}
}

func TestComputeHealthScoreTTFTP95Boundaries(t *testing.T) {
	t.Parallel()
	sample := BuildSample(100, 0, DefaultMinSample)
	tests := []struct {
		name    string
		p95Ms   int64
		hasTTFT bool
		want    int
	}{
		{name: "no TTFT uses error score only", want: 100},
		{name: "one second", p95Ms: 1_000, hasTTFT: true, want: 100},
		{name: "two seconds", p95Ms: 2_000, hasTTFT: true, want: 80},
		{name: "three seconds", p95Ms: 3_000, hasTTFT: true, want: 60},
		{name: "above three seconds", p95Ms: 82_000, hasTTFT: true, want: 60},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			score := ComputeHealthScore(sample, 0, test.p95Ms, test.hasTTFT)
			if score == nil || *score != test.want {
				t.Fatalf("score = %v, want %d", score, test.want)
			}
		})
	}
}

func TestBuildEntityRowUsesP95InsteadOfPeak(t *testing.T) {
	t.Parallel()
	row := buildEntityRow(entityBuildInput{
		ID:     1,
		Window: Window1h,
		Success: SuccessAgg{
			Count:       20,
			TTFTCount:   20,
			AvgTTFT:     5_050,
			P95TTFT:     1_000,
			MaxTTFT:     82_000,
			P95Duration: 6_000,
			MaxDuration: 90_000,
		},
		MinSample: DefaultMinSample,
	})
	if row.HealthScore == nil || *row.HealthScore != 100 {
		t.Fatalf("health score = %v, want 100 from P95=1s", row.HealthScore)
	}
	if row.TTFT.P95Ms != 1_000 || row.TTFT.MaxMs != 82_000 {
		t.Fatalf("TTFT = %+v, want P95=1000 max=82000", row.TTFT)
	}
}

func TestRatesAndSLAErrorCount(t *testing.T) {
	t.Parallel()
	sample := BuildSample(80, 20, 10)
	sr, er := Rates(sample)
	if sr != 0.8 || er != 0.2 {
		t.Fatalf("rates sr=%v er=%v", sr, er)
	}
	c := Counts{Auth: 1, RateLimit: 2, Upstream5xx: 3, Client: 9, Precheck: 100}
	if SLAErrorCount(c) != 6 {
		t.Fatalf("sla errors=%d", SLAErrorCount(c))
	}
}
