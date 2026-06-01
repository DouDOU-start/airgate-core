package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"entgo.io/ent/dialect/sql/schema"
	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"

	"github.com/DouDOU-start/airgate-core/ent"
	"github.com/DouDOU-start/airgate-core/ent/enttest"
	"github.com/DouDOU-start/airgate-core/internal/auth"
)

var ccCompatTestUserSeq int64

func TestCCCompatUserBalanceUsesUserBalanceForUnlimitedKey(t *testing.T) {
	db := openCCCompatTestDB(t)
	ctx := context.Background()
	key := createCCCompatTestKey(t, ctx, db, 12.34, 0, 99)

	resp := requestCCCompatBalance(t, db, key)
	requireStatus(t, resp, http.StatusOK)

	body := decodeCCCompatBody(t, resp)
	requireFloat(t, body["balance"], 12.34)
	requireFloat(t, body["remaining"], 12.34)
	if body["is_active"] != true {
		t.Fatalf("is_active = %v, want true", body["is_active"])
	}

	quota := body["quota"].(map[string]any)
	requireFloat(t, quota["remaining"], 12.34)
	if quota["unlimited"] != true {
		t.Fatalf("quota.unlimited = %v, want true", quota["unlimited"])
	}
}

func TestCCCompatUserBalanceUsesSmallerQuotaOrUserBalance(t *testing.T) {
	db := openCCCompatTestDB(t)
	ctx := context.Background()

	t.Run("quota remaining caps balance", func(t *testing.T) {
		key := createCCCompatTestKey(t, ctx, db, 50, 10, 3)
		resp := requestCCCompatBalance(t, db, key)
		requireStatus(t, resp, http.StatusOK)

		body := decodeCCCompatBody(t, resp)
		requireFloat(t, body["balance"], 7)
		requireFloat(t, body["remaining"], 7)

		quota := body["quota"].(map[string]any)
		requireFloat(t, quota["remaining"], 7)
		requireFloat(t, quota["total"], 10)
		requireFloat(t, quota["used"], 3)
	})

	t.Run("user balance caps quota remaining", func(t *testing.T) {
		key := createCCCompatTestKey(t, ctx, db, 2, 10, 3)
		resp := requestCCCompatBalance(t, db, key)
		requireStatus(t, resp, http.StatusOK)

		body := decodeCCCompatBody(t, resp)
		requireFloat(t, body["balance"], 2)
		requireFloat(t, body["remaining"], 2)

		quota := body["quota"].(map[string]any)
		requireFloat(t, quota["remaining"], 2)
		requireFloat(t, quota["api_key_remaining"], 7)
	})
}

func openCCCompatTestDB(t *testing.T) *ent.Client {
	t.Helper()
	db := enttest.Open(t, "sqlite3", "file:cc_compat?mode=memory&cache=shared&_fk=1", enttest.WithMigrateOptions(schema.WithGlobalUniqueID(false)))
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	})
	return db
}

func createCCCompatTestKey(t *testing.T, ctx context.Context, db *ent.Client, userBalance, quotaUSD, usedQuota float64) string {
	t.Helper()
	seq := atomic.AddInt64(&ccCompatTestUserSeq, 1)
	user, err := db.User.Create().
		SetEmail(fmt.Sprintf("cc-compat-%d@example.com", seq)).
		SetPasswordHash("hash").
		SetBalance(userBalance).
		Save(ctx)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	key, hash, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := db.APIKey.Create().
		SetName("cc-switch").
		SetKeyHash(hash).
		SetQuotaUsd(quotaUSD).
		SetUsedQuota(usedQuota).
		SetUser(user).
		Save(ctx); err != nil {
		t.Fatalf("create api key: %v", err)
	}
	return key
}

func requestCCCompatBalance(t *testing.T, db *ent.Client, key string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	s := &Server{db: db}
	router.GET("/v1/usage", s.handleCCCompatUserBalance)

	req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func decodeCCCompatBody(t *testing.T, resp *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, resp.Body.String())
	}
	return body
}

func requireStatus(t *testing.T, resp *httptest.ResponseRecorder, want int) {
	t.Helper()
	if resp.Code != want {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, want, resp.Body.String())
	}
}

func requireFloat(t *testing.T, got any, want float64) {
	t.Helper()
	value, ok := got.(float64)
	if !ok {
		t.Fatalf("value = %#v (%T), want float64 %.8f", got, got, want)
	}
	if math.Abs(value-want) > 1e-9 {
		t.Fatalf("value = %.8f, want %.8f", value, want)
	}
}
