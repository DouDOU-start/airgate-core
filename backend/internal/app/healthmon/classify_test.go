package healthmon

import "testing"

func TestClassifyFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		phase  string
		want   ErrorClass
		sla    bool
	}{
		{"401 auth", 401, "upstream_exhausted", ClassAuth, true},
		{"402 auth", 402, "", ClassAuth, true},
		{"403 auth", 403, "", ClassAuth, true},
		{"429 rate", 429, "upstream_exhausted", ClassRateLimit, true},
		{"500 upstream", 500, "", ClassUpstream5xx, true},
		{"503 upstream", 503, "upstream_exhausted", ClassUpstream5xx, true},
		{"408 timeout", 408, "", ClassUpstream5xx, true},
		{"400 client", 400, "upstream_client_error", ClassClient, false},
		{"phase precheck balance", 402, "precheck_balance", ClassPrecheck, false},
		{"phase precheck price", 400, "precheck_price", ClassPrecheck, false},
		{"phase moderation", 403, "precheck_moderation", ClassPrecheck, false},
		{"phase canceled", 499, "canceled", ClassCanceled, false},
		{"phase stream aborted", 0, "stream_aborted", ClassUpstream5xx, true},
		{"network no status", 0, "upstream_exhausted", ClassUpstream5xx, true},
		{"local limit", 429, "local_limit", ClassPrecheck, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyFailure(tc.status, tc.phase)
			if got != tc.want {
				t.Fatalf("ClassifyFailure(%d,%q)=%s want %s", tc.status, tc.phase, got, tc.want)
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

func TestRatesAndSLAErrorCount(t *testing.T) {
	t.Parallel()
	sample := BuildSample(80, 20, 10)
	sr, er := Rates(sample)
	if sr != 0.8 || er != 0.2 {
		t.Fatalf("rates sr=%v er=%v", sr, er)
	}
	c := Counts{Auth: 1, RateLimit: 2, Upstream5xx: 3, Client: 9}
	if SLAErrorCount(c) != 6 {
		t.Fatalf("sla errors=%d", SLAErrorCount(c))
	}
}
