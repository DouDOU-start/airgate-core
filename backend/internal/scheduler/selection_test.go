package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/DouDOU-start/airgate-core/ent"
)

func TestExcludeAccountsDoesNotMutateCandidates(t *testing.T) {
	t.Parallel()

	candidates := []*ent.Account{{ID: 1}, {ID: 2}, {ID: 3}}
	got := excludeAccounts(candidates, []int{2})

	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("excludeAccounts result = %+v, want IDs [1 3]", got)
	}
	if len(candidates) != 3 || candidates[0].ID != 1 || candidates[1].ID != 2 || candidates[2].ID != 3 {
		t.Fatalf("candidates mutated to %+v, want original IDs [1 2 3]", candidates)
	}
}

func TestSelectSoftStickyAccountHonorsHighestPriority(t *testing.T) {
	t.Parallel()

	candidates := []*ent.Account{
		{ID: 1, Priority: 10},
		{ID: 2, Priority: 20},
		{ID: 3, Priority: 20},
	}

	if got := selectSoftStickyAccount(candidates, 1); got != nil {
		t.Fatalf("low priority sticky account selected: %+v", got)
	}
	if got := selectSoftStickyAccount(candidates, 2); got == nil || got.ID != 2 {
		t.Fatalf("top priority sticky account = %+v, want account 2", got)
	}
}

func TestSoftStickyPrefersNormalPriorityPool(t *testing.T) {
	t.Parallel()

	normalCandidates := []*ent.Account{{ID: 2, Priority: 20}}
	stickyCandidates := []*ent.Account{
		{ID: 1, Priority: 30},
		{ID: 2, Priority: 20},
	}

	pool := softStickyCandidates(normalCandidates, stickyCandidates)
	if got := selectSoftStickyAccount(pool, 1); got != nil {
		t.Fatalf("sticky-only account selected while normal candidates exist: %+v", got)
	}
	if got := selectSoftStickyAccount(pool, 2); got == nil || got.ID != 2 {
		t.Fatalf("normal top priority sticky account = %+v, want account 2", got)
	}
}

func TestNormalizeGroupLookupErrorPreservesCancellation(t *testing.T) {
	t.Parallel()

	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		got := normalizeGroupLookupError(err)
		if !errors.Is(got, err) {
			t.Fatalf("normalizeGroupLookupError(%v) = %v, want original error", err, got)
		}
	}
}

func TestNormalizeGroupLookupErrorWrapsGenericError(t *testing.T) {
	t.Parallel()

	orig := errors.New("db offline")
	got := normalizeGroupLookupError(orig)
	if errors.Is(got, ErrGroupNotFound) {
		t.Fatalf("normalizeGroupLookupError(%v) = %v, want generic query error", orig, got)
	}
	if got.Error() != "查询分组失败: db offline" {
		t.Fatalf("normalizeGroupLookupError(%v) = %q, want %q", orig, got.Error(), "查询分组失败: db offline")
	}
}

func TestNormalizeGroupAccountsLookupErrorPreservesCancellation(t *testing.T) {
	t.Parallel()

	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		got := normalizeGroupAccountsLookupError(err)
		if !errors.Is(got, err) {
			t.Fatalf("normalizeGroupAccountsLookupError(%v) = %v, want original error", err, got)
		}
	}
}

func TestNormalizeGroupAccountsLookupErrorWrapsGenericError(t *testing.T) {
	t.Parallel()

	orig := errors.New("db offline")
	got := normalizeGroupAccountsLookupError(orig)
	if got.Error() != "查询分组账户失败: db offline" {
		t.Fatalf("normalizeGroupAccountsLookupError(%v) = %q, want %q", orig, got.Error(), "查询分组账户失败: db offline")
	}
}
