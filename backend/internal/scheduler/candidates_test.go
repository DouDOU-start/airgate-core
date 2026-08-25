package scheduler

import (
	"testing"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/account"
)

func TestRouteCandidateSnapshotOnlyExposesCurrentlyNormalAccounts(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Minute)
	past := now.Add(-time.Minute)
	tests := []struct {
		name    string
		account *ent.Account
		want    bool
	}{
		{name: "active", account: &ent.Account{ID: 1, State: account.StateActive}, want: true},
		{name: "rate limited", account: &ent.Account{ID: 2, State: account.StateRateLimited, StateUntil: &future}},
		{name: "degraded", account: &ent.Account{ID: 3, State: account.StateDegraded, StateUntil: &future}},
		{name: "disabled", account: &ent.Account{ID: 4, State: account.StateDisabled}},
		{name: "expired rate limit", account: &ent.Account{ID: 5, State: account.StateRateLimited, StateUntil: &past}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate, ok := routeCandidateSnapshot(tt.account, now)
			if ok != tt.want {
				t.Fatalf("ok = %v, want %v", ok, tt.want)
			}
			if ok && candidate.State != "active" {
				t.Fatalf("candidate state = %q", candidate.State)
			}
		})
	}
}
