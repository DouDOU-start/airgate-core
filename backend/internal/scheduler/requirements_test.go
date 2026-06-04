package scheduler

import (
	"testing"

	"github.com/DouDOU-start/airgate-core/ent"
)

func TestFilterAccountsByRequirements_Workload(t *testing.T) {
	candidates := []*ent.Account{
		{ID: 1, Extra: map[string]interface{}{"allowed_workloads": []interface{}{"chat"}}},
		{ID: 2, Extra: map[string]interface{}{"allowed_workloads": []interface{}{"image"}}},
		{ID: 3, Extra: map[string]interface{}{"allowed_workloads": []interface{}{"chat", "image"}}},
		{ID: 4},
	}

	got := filterAccountsByRequirements(candidates, AccountRequirements{Workload: WorkloadImage})
	wantIDs(t, got, []int{2, 3, 4})

	got = filterAccountsByRequirements(candidates, AccountRequirements{Workload: WorkloadChat})
	wantIDs(t, got, []int{1, 3, 4})
}

func TestFilterAccountsByRequirements_ImageProtocols(t *testing.T) {
	candidates := []*ent.Account{
		{ID: 1, Extra: map[string]interface{}{"image_protocols": []interface{}{"images_api"}}},
		{ID: 2, Extra: map[string]interface{}{"image_protocols": []interface{}{"responses_tool"}}},
		{ID: 3, Extra: map[string]interface{}{"image_protocols": "images_api,responses_tool"}},
		{ID: 4, Credentials: map[string]string{"api_key": "sk-test"}},
		{ID: 5, Credentials: map[string]string{"access_token": "token"}},
	}

	got := filterAccountsByRequirements(candidates, AccountRequirements{
		Workload:       WorkloadImage,
		ImageProtocols: []ImageProtocol{ImageProtocolResponsesTool},
	})
	wantIDs(t, got, []int{2, 3, 5})

	got = filterAccountsByRequirements(candidates, AccountRequirements{
		Workload:       WorkloadImage,
		ImageProtocols: []ImageProtocol{ImageProtocolImagesAPI},
	})
	wantIDs(t, got, []int{1, 3, 4})
}

func wantIDs(t *testing.T, got []*ent.Account, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d; got=%+v", len(got), len(want), got)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("got[%d].ID = %d, want %d; got=%+v", i, got[i].ID, id, got)
		}
	}
}
