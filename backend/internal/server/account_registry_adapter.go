package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/DouDOU-start/airgate-core/ent"
	entaccount "github.com/DouDOU-start/airgate-core/ent/account"
	"github.com/DouDOU-start/airgate-core/internal/auth"
	"github.com/DouDOU-start/airgate-core/internal/relay/accountreg"
	"github.com/DouDOU-start/airgate-core/internal/relay/cpa"
)

// accountRegistryAdapter 将 ent + AES 解密桥接到 accountreg.Loader/Persister。
// 不经过 app 层，避免 relay 依赖环；仅 server 装配使用。
type accountRegistryAdapter struct {
	db     *ent.Client
	secret string
}

// LoadAllForAccountRegistry 全量加载账号快照（解密凭证、拼代理 URL、填模型集）。
func (a *accountRegistryAdapter) LoadAllForAccountRegistry(ctx context.Context) ([]accountreg.Snapshot, error) {
	items, err := a.db.Account.Query().
		WithGroups().
		WithProxy().
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]accountreg.Snapshot, 0, len(items))
	for _, item := range items {
		snap, err := a.mapSnapshot(item)
		if err != nil {
			// 单条解密失败不阻断全量加载，跳过并继续。
			continue
		}
		out = append(out, snap)
	}
	return out, nil
}

// PersistAccountState 异步落库账号状态。
func (a *accountRegistryAdapter) PersistAccountState(ctx context.Context, accountID int, state string, stateUntil *time.Time, errMsg string) error {
	builder := a.db.Account.UpdateOneID(accountID).
		SetState(entaccount.State(state)).
		SetErrorMsg(errMsg)
	if stateUntil == nil {
		builder = builder.ClearStateUntil()
	} else {
		builder = builder.SetStateUntil(*stateUntil)
	}
	_, err := builder.Save(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	return err
}

// PersistAccountCredentials refresh 后写回加密凭证。
func (a *accountRegistryAdapter) PersistAccountCredentials(ctx context.Context, accountID int, credentials map[string]string) error {
	if len(credentials) == 0 {
		return nil
	}
	// 与存量合并：读出旧密文解密后 overlay。
	item, err := a.db.Account.Get(ctx, accountID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return err
	}
	merged := map[string]string{}
	if item.CredentialsEnc != "" {
		plain, err := auth.DecryptAPIKey(item.CredentialsEnc, a.secret)
		if err == nil {
			// 简易 JSON 对象解析
			merged = decodeCredMap(plain)
		}
	}
	for k, v := range credentials {
		if v == "" {
			delete(merged, k)
			continue
		}
		merged[k] = v
	}
	raw, err := encodeCredMap(merged)
	if err != nil {
		return err
	}
	enc, err := auth.EncryptAPIKey(raw, a.secret)
	if err != nil {
		return err
	}
	email := merged["email"]
	_, err = a.db.Account.UpdateOneID(accountID).
		SetCredentialsEnc(enc).
		SetEmail(email).
		Save(ctx)
	return err
}

func (a *accountRegistryAdapter) mapSnapshot(item *ent.Account) (accountreg.Snapshot, error) {
	creds := map[string]string{}
	if item.CredentialsEnc != "" {
		plain, err := auth.DecryptAPIKey(item.CredentialsEnc, a.secret)
		if err != nil {
			return accountreg.Snapshot{}, err
		}
		creds = decodeCredMap(plain)
	}

	proxyURL := ""
	// 绑了代理但 disabled → 不可调度（fail-closed）：Models 置空即可。
	proxyDisabled := false
	if item.Edges.Proxy != nil {
		p := item.Edges.Proxy
		if p.Status.String() == "disabled" {
			proxyDisabled = true
		} else {
			password, err := auth.DecryptSecretValue(p.Password, a.secret)
			if err != nil {
				return accountreg.Snapshot{}, fmt.Errorf("解密代理 %d 的密码失败: %w", p.ID, err)
			}
			proxyURL = buildProxyURL(p.Protocol.String(), p.Address, p.Port, p.Username, password)
		}
	}

	groupIDs := map[int]struct{}{}
	for _, g := range item.Edges.Groups {
		groupIDs[g.ID] = struct{}{}
	}

	models := modelsFromExtra(item.Extra)
	if len(models) == 0 {
		// Codex 按 plan_type 分档（与 CPA service_models OAuth 逻辑一致）
		plan := strings.TrimSpace(creds["plan_type"])
		if plan == "" && item.Extra != nil {
			if p, ok := item.Extra["plan_type"].(string); ok {
				plan = strings.TrimSpace(p)
			}
		}
		models = cpa.DefaultModelSetWithPlan(item.Platform, plan)
	}
	if proxyDisabled {
		models = map[string]struct{}{} // 不可调度
	}

	maxRPM := 0
	if item.Extra != nil {
		if v, ok := item.Extra["max_rpm"]; ok {
			maxRPM = anyToInt(v)
		}
	}

	snap := accountreg.Snapshot{
		ID:             item.ID,
		Name:           item.Name,
		Email:          item.Email,
		Platform:       item.Platform,
		Type:           item.Type,
		Credentials:    creds,
		ProxyURL:       proxyURL,
		Priority:       item.Priority,
		Weight:         item.Weight,
		MaxConcurrency: item.MaxConcurrency,
		MaxRPM:         maxRPM,
		RateMultiplier: item.RateMultiplier,
		State:          item.State.String(),
		ErrorMsg:       item.ErrorMsg,
		UpstreamIsPool: item.UpstreamIsPool,
		Models:         models,
		GroupIDs:       groupIDs,
	}
	if item.StateUntil != nil {
		t := *item.StateUntil
		snap.StateUntil = &t
	}
	return snap, nil
}

func buildProxyURL(protocol, address string, port int, username, password string) string {
	if address == "" || port <= 0 {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(protocol))
	if scheme == "" {
		scheme = "http"
	}
	host := address
	if port > 0 {
		host = fmt.Sprintf("%s:%d", address, port)
	}
	u := &url.URL{Scheme: scheme, Host: host}
	if username != "" || password != "" {
		u.User = url.UserPassword(username, password)
	}
	return u.String()
}

func modelsFromExtra(extra map[string]interface{}) map[string]struct{} {
	if extra == nil {
		return nil
	}
	raw, ok := extra["models"]
	if !ok || raw == nil {
		return nil
	}
	set := map[string]struct{}{}
	switch v := raw.(type) {
	case []string:
		for _, m := range v {
			m = strings.TrimSpace(m)
			if m != "" {
				set[m] = struct{}{}
			}
		}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					set[s] = struct{}{}
				}
			}
		}
	case string:
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				set[part] = struct{}{}
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func anyToInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	default:
		return 0
	}
}

func decodeCredMap(plain string) map[string]string {
	out := map[string]string{}
	_ = json.Unmarshal([]byte(plain), &out)
	if out == nil {
		out = map[string]string{}
	}
	return out
}

func encodeCredMap(m map[string]string) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
