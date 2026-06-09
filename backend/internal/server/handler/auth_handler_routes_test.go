package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	appauth "github.com/DouDOU-start/airgate-core/internal/app/auth"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/infra/mailer"
)

func TestVerifyCodeRejectsInvalidCode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store := mailer.NewVerifyCodeStore()
	code := store.Generate("user@example.com")
	wrongCode := "000000"
	if code == wrongCode {
		wrongCode = "111111"
	}

	// 构造 auth service 并注入验证码存储
	jwtMgr := auth.NewJWTManager("secret", 24)
	authService := appauth.NewService(stubAuthRepo{}, jwtMgr)
	authService.SetVerifyCodeStore(store)

	router := gin.New()
	handler := NewAuthHandler(authService, jwtMgr)
	router.POST("/verify-code", handler.VerifyCode)

	body := `{"email":"user@example.com","code":"` + wrongCode + `"}`
	req := httptest.NewRequest(http.MethodPost, "/verify-code", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("VerifyCode invalid code status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !store.Check("user@example.com", code) {
		t.Fatal("invalid verification attempt should not consume the valid code")
	}
}

func TestVerifyCodeDoesNotConsumeValidCode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store := mailer.NewVerifyCodeStore()
	code := store.Generate("user@example.com")

	// 构造 auth service 并注入验证码存储
	jwtMgr := auth.NewJWTManager("secret", 24)
	authService := appauth.NewService(stubAuthRepo{}, jwtMgr)
	authService.SetVerifyCodeStore(store)

	router := gin.New()
	handler := NewAuthHandler(authService, jwtMgr)
	router.POST("/verify-code", handler.VerifyCode)

	body := `{"email":"user@example.com","code":"` + code + `"}`
	req := httptest.NewRequest(http.MethodPost, "/verify-code", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("VerifyCode valid code status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !store.Check("user@example.com", code) {
		t.Fatal("first-step verification should not consume the code; register still needs to verify it")
	}
	if !store.Verify("user@example.com", code) {
		t.Fatal("registration should still be able to consume the verified code")
	}
	if store.Check("user@example.com", code) {
		t.Fatal("registration verification should consume the code")
	}
}

// stubAuthRepo 空仓储桩（测试中不需要访问数据库）。
type stubAuthRepo struct{}

func (stubAuthRepo) FindByEmail(_ context.Context, _ string) (appauth.User, error) {
	return appauth.User{}, appauth.ErrUserNotFound
}
func (stubAuthRepo) EmailExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (stubAuthRepo) Create(_ context.Context, _ appauth.CreateUserInput) (appauth.User, error) {
	return appauth.User{}, nil
}
func (stubAuthRepo) FindByID(_ context.Context, _ int, _ bool) (appauth.User, error) {
	return appauth.User{}, appauth.ErrUserNotFound
}
func (stubAuthRepo) ValidateAPIKeySession(_ context.Context, _, _ int) (appauth.User, error) {
	return appauth.User{}, appauth.ErrInvalidAPIKeySession
}
func (stubAuthRepo) ValidateAPIKeyForLogin(_ context.Context, _ string) (appauth.APIKeyLoginInfo, error) {
	return appauth.APIKeyLoginInfo{}, appauth.ErrInvalidAPIKey
}
func (stubAuthRepo) GetAPIKeyBrief(_ context.Context, _ int) (appauth.APIKeyBrief, error) {
	return appauth.APIKeyBrief{}, nil
}
