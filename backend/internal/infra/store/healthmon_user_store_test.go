package store

import (
	"context"
	"testing"
	"time"

	entupstreamrequestlog "github.com/DouDOU-start/airgate-core/ent/upstreamrequestlog"
)

func TestHealthmonStoreListUserVisibleGroupMetaEnforcesAccessAndVisibility(t *testing.T) {
	db := enttestOpen(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()
	ctx := context.Background()
	user := createTestUser(t, db, "healthmon-visible@example.com")
	other := createTestUser(t, db, "healthmon-other@example.com")

	publicVisible, err := db.Group.Create().
		SetName("public-visible").
		SetPlatform("openai").
		SetStatusVisible(true).
		Save(ctx)
	if err != nil {
		t.Fatalf("create public visible group: %v", err)
	}
	publicHidden, err := db.Group.Create().
		SetName("public-hidden").
		SetStatusVisible(false).
		Save(ctx)
	if err != nil {
		t.Fatalf("create public hidden group: %v", err)
	}
	exclusiveAllowed, err := db.Group.Create().
		SetName("exclusive-allowed").
		SetIsExclusive(true).
		SetStatusVisible(true).
		AddAllowedUserIDs(user.ID).
		Save(ctx)
	if err != nil {
		t.Fatalf("create allowed exclusive group: %v", err)
	}
	exclusiveOther, err := db.Group.Create().
		SetName("exclusive-other").
		SetIsExclusive(true).
		SetStatusVisible(true).
		AddAllowedUserIDs(other.ID).
		Save(ctx)
	if err != nil {
		t.Fatalf("create other exclusive group: %v", err)
	}
	exclusiveHidden, err := db.Group.Create().
		SetName("exclusive-hidden").
		SetIsExclusive(true).
		SetStatusVisible(false).
		AddAllowedUserIDs(user.ID).
		Save(ctx)
	if err != nil {
		t.Fatalf("create hidden exclusive group: %v", err)
	}

	store := NewHealthmonStore(db)
	metas, err := store.ListUserVisibleGroupMeta(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListUserVisibleGroupMeta returned error: %v", err)
	}
	got := make(map[int]bool, len(metas))
	for _, meta := range metas {
		got[meta.ID] = true
	}
	if len(got) != 2 || !got[publicVisible.ID] || !got[exclusiveAllowed.ID] {
		t.Fatalf("visible ids = %v, want public %d and allowed exclusive %d", got, publicVisible.ID, exclusiveAllowed.ID)
	}
	for _, forbidden := range []int{publicHidden.ID, exclusiveOther.ID, exclusiveHidden.ID} {
		if got[forbidden] {
			t.Fatalf("forbidden group %d leaked into visible metadata: %v", forbidden, got)
		}
	}

	metas, err = store.ListUserVisibleGroupMeta(ctx, 0)
	if err != nil {
		t.Fatalf("zero-user visibility returned error: %v", err)
	}
	if len(metas) != 0 {
		t.Fatalf("zero-user visibility = %+v, want empty", metas)
	}
}

func TestHealthmonStoreGroupIDAggregatesNeverFallBackToAllGroups(t *testing.T) {
	db := enttestOpen(t)
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()
	ctx := context.Background()
	visible, err := db.Group.Create().SetName("aggregate-visible").Save(ctx)
	if err != nil {
		t.Fatalf("create visible group: %v", err)
	}
	hidden, err := db.Group.Create().SetName("aggregate-hidden").Save(ctx)
	if err != nil {
		t.Fatalf("create hidden group: %v", err)
	}
	at := time.Date(2026, 8, 8, 11, 30, 0, 0, time.UTC)
	since := at.Add(-time.Hour)

	for _, item := range []struct {
		groupID int
		source  string
	}{
		{groupID: visible.ID, source: "relay"},
		{groupID: hidden.ID, source: "relay"},
		{groupID: visible.ID, source: "channel_test"},
	} {
		if _, err := db.UsageLog.Create().
			SetModel("gpt-test").
			SetGroupID(item.groupID).
			SetSource(item.source).
			SetDurationMs(1200).
			SetFirstTokenMs(400).
			SetCreatedAt(at).
			Save(ctx); err != nil {
			t.Fatalf("create usage log: %v", err)
		}
	}

	createFailure := func(requestID string, groupID int, source entupstreamrequestlog.Source, billed bool, repeat int) {
		t.Helper()
		if _, err := db.UpstreamRequestLog.Create().
			SetRequestID(requestID).
			SetSource(source).
			SetGroupID(groupID).
			SetStatusCode(429).
			SetBilled(billed).
			SetRepeatCount(repeat).
			SetCreatedAt(at).
			Save(ctx); err != nil {
			t.Fatalf("create upstream failure: %v", err)
		}
	}
	createFailure("visible-relay", visible.ID, entupstreamrequestlog.SourceRelay, false, 3)
	createFailure("hidden-relay", hidden.ID, entupstreamrequestlog.SourceRelay, false, 7)
	createFailure("visible-billed", visible.ID, entupstreamrequestlog.SourceRelay, true, 11)
	createFailure("visible-test", visible.ID, entupstreamrequestlog.SourceChannelTest, false, 13)

	store := NewHealthmonStore(db)
	success, err := store.AggregateSuccessByGroupIDs(ctx, since, []int{visible.ID})
	if err != nil {
		t.Fatalf("AggregateSuccessByGroupIDs returned error: %v", err)
	}
	if len(success) != 1 || success[0].DimID != visible.ID || success[0].Count != 1 {
		t.Fatalf("success aggregate = %+v, want one visible relay row", success)
	}
	failures, err := store.AggregateFailureRawsByGroupIDs(ctx, since, []int{visible.ID})
	if err != nil {
		t.Fatalf("AggregateFailureRawsByGroupIDs returned error: %v", err)
	}
	if len(failures) != 2 {
		t.Fatalf("failure aggregate = %+v, want billed and unbilled visible rows", failures)
	}
	countsByBilled := make(map[bool]int64, len(failures))
	for _, failure := range failures {
		if failure.GroupID != visible.ID {
			t.Fatalf("hidden group leaked into failure aggregate: %+v", failures)
		}
		countsByBilled[failure.Billed] += failure.Count
	}
	if countsByBilled[false] != 3 || countsByBilled[true] != 11 {
		t.Fatalf("failure counts by billed = %v, want false:3 true:11", countsByBilled)
	}

	emptySuccess, err := store.AggregateSuccessByGroupIDs(ctx, since, nil)
	if err != nil {
		t.Fatalf("empty success aggregate returned error: %v", err)
	}
	emptyFailures, err := store.AggregateFailureRawsByGroupIDs(ctx, since, nil)
	if err != nil {
		t.Fatalf("empty failure aggregate returned error: %v", err)
	}
	if len(emptySuccess) != 0 || len(emptyFailures) != 0 {
		t.Fatalf("empty allow-list fell back to all groups: success=%+v failures=%+v", emptySuccess, emptyFailures)
	}
}
