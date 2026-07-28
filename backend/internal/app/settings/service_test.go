package settings

import (
	"context"
	"errors"
	"testing"
)

func TestUpdateClonesInput(t *testing.T) {
	var captured []ItemInput
	service := NewService(settingsStubRepository{
		upsertMany: func(_ context.Context, items []ItemInput) error {
			captured = append(captured, items...)
			return nil
		},
	}, "")

	input := []ItemInput{{Key: "site_name", Value: "Airgate"}}
	if err := service.Update(t.Context(), input); err != nil {
		t.Fatalf("Update() returned error: %v", err)
	}

	input[0].Value = "Changed"
	if captured[0].Value != "Airgate" {
		t.Fatalf("captured value = %q, want Airgate", captured[0].Value)
	}
}

// TestUpdateMaskedSentinelSemantics 掩码键写路径三分支：
// 哨兵值跳过（保持现值）/ 空串落库（真实清空）/ 新值落库。
func TestUpdateMaskedSentinelSemantics(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  []string // 期望落库的 value 序列（nil = 不落库）
	}{
		{"哨兵保持", MaskedValue, nil},
		{"空串清空", "", []string{""}},
		{"新值落库", "new-pass", []string{"new-pass"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured []ItemInput
			service := NewService(settingsStubRepository{
				upsertMany: func(_ context.Context, items []ItemInput) error {
					captured = append(captured, items...)
					return nil
				},
			}, "")

			input := []ItemInput{{Key: "smtp_password", Value: tc.value, Group: "smtp"}}
			if err := service.Update(t.Context(), input); err != nil {
				t.Fatalf("Update() returned error: %v", err)
			}
			if len(captured) != len(tc.want) {
				t.Fatalf("落库条数 = %d, want %d (%+v)", len(captured), len(tc.want), captured)
			}
			for i, want := range tc.want {
				if captured[i].Value != want {
					t.Fatalf("落库 value = %q, want %q", captured[i].Value, want)
				}
			}
		})
	}
}

// TestUpdateRejectsSecurityKeys security 组键（含换 group 绕过）经通用 Update 拒写。
func TestUpdateRejectsSecurityKeys(t *testing.T) {
	cases := []struct {
		name string
		item ItemInput
	}{
		{"security_group", ItemInput{Key: "some_key", Value: "v", Group: "security"}},
		{"security_key_other_group", ItemInput{Key: "admin_api_key_hash", Value: "v", Group: "site"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			service := NewService(settingsStubRepository{
				upsertMany: func(context.Context, []ItemInput) error {
					called = true
					return nil
				},
			}, "")

			err := service.Update(t.Context(), []ItemInput{tc.item})
			if !errors.Is(err, ErrSecuritySettingReadOnly) {
				t.Fatalf("期望 ErrSecuritySettingReadOnly, got %v", err)
			}
			if called {
				t.Fatal("security 键不应触发落库")
			}
		})
	}
}

// TestListMasked 管理端读路径：security 组不返回；
// 掩码键已配置回哨兵值、未配置回空串；普通键原样返回。
func TestListMasked(t *testing.T) {
	items := []Setting{
		{Key: "site_name", Value: "AirGate", Group: "site"},
		{Key: "smtp_password", Value: "super-secret", Group: "smtp"},
		{Key: "wechat_app_secret", Value: "wechat-secret", Group: "wechat"},
		{Key: "admin_api_key_hash", Value: "hash", Group: "security"},
	}
	service := NewService(settingsStubRepository{
		list: func(context.Context, string) ([]Setting, error) { return items, nil },
	}, "")

	got, err := service.ListMasked(t.Context(), "")
	if err != nil {
		t.Fatalf("ListMasked() returned error: %v", err)
	}
	byKey := map[string]Setting{}
	for _, s := range got {
		byKey[s.Key] = s
	}
	if _, ok := byKey["admin_api_key_hash"]; ok {
		t.Fatal("security 组键不应返回")
	}
	if byKey["smtp_password"].Value != MaskedValue {
		t.Fatalf("已配置掩码键应回哨兵值, got %q", byKey["smtp_password"].Value)
	}
	if byKey["wechat_app_secret"].Value != MaskedValue {
		t.Fatalf("微信公众号 AppSecret 应回掩码哨兵，got %q", byKey["wechat_app_secret"].Value)
	}
	if byKey["site_name"].Value != "AirGate" {
		t.Fatalf("普通键应原样返回, got %q", byKey["site_name"].Value)
	}

	// 未配置（空串）的掩码键回空串，前端据此区分"未配置"与"已配置需保持"
	items = []Setting{{Key: "smtp_password", Value: "", Group: "smtp"}}
	got, err = service.ListMasked(t.Context(), "")
	if err != nil {
		t.Fatalf("ListMasked() returned error: %v", err)
	}
	if len(got) != 1 || got[0].Value != "" {
		t.Fatalf("未配置掩码键应回空串, got %+v", got)
	}
}

type captureWeChatTester struct {
	input TestWeChatInput
}

func (t *captureWeChatTester) SendTest(_ context.Context, input TestWeChatInput) error {
	t.input = input
	return nil
}

func TestWeChatResolvesMaskedAppSecret(t *testing.T) {
	service := NewService(settingsStubRepository{
		list: func(_ context.Context, group string) ([]Setting, error) {
			if group != "wechat" {
				t.Fatalf("应读取 wechat 组，got %q", group)
			}
			return []Setting{{Key: "wechat_app_secret", Value: "stored-secret", Group: "wechat"}}, nil
		},
	}, "")
	tester := &captureWeChatTester{}
	service.SetWeChatTester(tester)

	err := service.TestWeChat(t.Context(), TestWeChatInput{
		AppID:      "wx-test",
		AppSecret:  MaskedValue,
		TemplateID: "template-1",
		OpenID:     "openid-1",
	})
	if err != nil {
		t.Fatalf("TestWeChat() 返回错误：%v", err)
	}
	if tester.input.AppSecret != "stored-secret" {
		t.Fatalf("AppSecret = %q，期望读取存量值", tester.input.AppSecret)
	}
}

// TestResolveTestSMTPPassword TestSMTP 密码回退：哨兵值 → 读库中存量密码；
// 其余（含空串）原样使用。
func TestResolveTestSMTPPassword(t *testing.T) {
	service := NewService(settingsStubRepository{
		list: func(_ context.Context, group string) ([]Setting, error) {
			if group != "smtp" {
				t.Fatalf("应读取 smtp 组, got %q", group)
			}
			return []Setting{{Key: "smtp_password", Value: "stored-pass", Group: "smtp"}}, nil
		},
	}, "")

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"哨兵回退存量密码", MaskedValue, "stored-pass"},
		{"空串按空密码测试", "", ""},
		{"新密码原样使用", "typed-pass", "typed-pass"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := service.resolveTestSMTPPassword(t.Context(), tc.input)
			if err != nil {
				t.Fatalf("resolveTestSMTPPassword() returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("password = %q, want %q", got, tc.want)
			}
		})
	}

	// 读库失败须报错（而非拿空密码静默测试造成误判）
	failing := NewService(settingsStubRepository{
		list: func(context.Context, string) ([]Setting, error) {
			return nil, errors.New("db down")
		},
	}, "")
	if _, err := failing.resolveTestSMTPPassword(t.Context(), MaskedValue); err == nil {
		t.Fatal("读库失败应返回错误")
	}
}

// TestSiteOGImage 分享卡片封面图读取：命中返回值（去空白）、
// 未配置该 key 或读库失败时都回退空串，交由调用方走默认封面。
func TestSiteOGImage(t *testing.T) {
	cases := []struct {
		name string
		list func(context.Context, string) ([]Setting, error)
		want string
	}{
		{
			name: "已配置",
			list: func(_ context.Context, group string) ([]Setting, error) {
				if group != "site" {
					t.Fatalf("应读取 site 组, got %q", group)
				}
				return []Setting{{Key: "og_image", Value: " /uploads/cover.png ", Group: "site"}}, nil
			},
			want: "/uploads/cover.png",
		},
		{
			name: "未配置该key",
			list: func(context.Context, string) ([]Setting, error) {
				return []Setting{{Key: "site_name", Value: "AirGate", Group: "site"}}, nil
			},
			want: "",
		},
		{
			name: "读库失败",
			list: func(context.Context, string) ([]Setting, error) {
				return nil, errors.New("db down")
			},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := NewService(settingsStubRepository{list: tc.list}, "")
			if got := service.SiteOGImage(t.Context()); got != tc.want {
				t.Fatalf("SiteOGImage() = %q, want %q", got, tc.want)
			}
		})
	}
}

type settingsStubRepository struct {
	list       func(context.Context, string) ([]Setting, error)
	upsertMany func(context.Context, []ItemInput) error
}

func (s settingsStubRepository) List(ctx context.Context, group string) ([]Setting, error) {
	if s.list == nil {
		return nil, nil
	}
	return s.list(ctx, group)
}

func (s settingsStubRepository) UpsertMany(ctx context.Context, items []ItemInput) error {
	if s.upsertMany == nil {
		return nil
	}
	return s.upsertMany(ctx, items)
}
