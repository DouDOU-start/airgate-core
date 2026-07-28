package wechat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestSendTemplateCachesAccessToken(t *testing.T) {
	var tokenCalls atomic.Int32
	var sendCalls atomic.Int32

	client := New(nil)
	client.baseURL = "https://wechat.test"
	client.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/cgi-bin/token":
			tokenCalls.Add(1)
			if request.URL.Query().Get("appid") != "wx-test" || request.URL.Query().Get("secret") != "secret" {
				t.Fatalf("凭证参数不正确：%s", request.URL.RawQuery)
			}
			return jsonResponse(`{"access_token":"token-1","expires_in":7200}`), nil
		case "/cgi-bin/message/template/send":
			sendCalls.Add(1)
			if request.URL.Query().Get("access_token") != "token-1" {
				t.Fatalf("发送消息使用了错误令牌：%s", request.URL.RawQuery)
			}
			var message TemplateMessage
			if err := json.NewDecoder(request.Body).Decode(&message); err != nil {
				t.Fatalf("解析模板消息失败：%v", err)
			}
			if message.ToUser != "openid-1" || message.TemplateID != "template-1" {
				t.Fatalf("模板消息参数不正确：%+v", message)
			}
			return jsonResponse(`{"errcode":0,"errmsg":"ok"}`), nil
		default:
			t.Fatalf("未预期的请求路径：%s", request.URL.Path)
			return nil, nil
		}
	})}

	message := TemplateMessage{
		ToUser:     "openid-1",
		TemplateID: "template-1",
		Data: map[string]TemplateData{
			"first": {Value: "测试消息"},
		},
	}
	credentials := Credentials{AppID: "wx-test", AppSecret: "secret"}

	for range 2 {
		if err := client.SendTemplate(context.Background(), credentials, message); err != nil {
			t.Fatalf("SendTemplate() 返回错误：%v", err)
		}
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("access_token 请求次数 = %d，期望 1", tokenCalls.Load())
	}
	if sendCalls.Load() != 2 {
		t.Fatalf("模板消息请求次数 = %d，期望 2", sendCalls.Load())
	}
}

func TestSendTemplateRefreshesExpiredToken(t *testing.T) {
	var tokenCalls atomic.Int32
	var sendCalls atomic.Int32

	client := New(nil)
	client.baseURL = "https://wechat.test"
	client.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/cgi-bin/token":
			call := tokenCalls.Add(1)
			return jsonResponse(`{"access_token":"token-` + strconv.Itoa(int(call)) + `","expires_in":7200}`), nil
		case "/cgi-bin/message/template/send":
			call := sendCalls.Add(1)
			if call == 1 {
				return jsonResponse(`{"errcode":42001,"errmsg":"access_token expired"}`), nil
			}
			if request.URL.Query().Get("access_token") != "token-2" {
				t.Fatalf("重试未使用刷新后的令牌：%s", request.URL.RawQuery)
			}
			return jsonResponse(`{"errcode":0,"errmsg":"ok"}`), nil
		default:
			t.Fatalf("未预期的请求路径：%s", request.URL.Path)
			return nil, nil
		}
	})}

	err := client.SendTemplate(context.Background(), Credentials{AppID: "wx-test", AppSecret: "secret"}, TemplateMessage{
		ToUser:     "openid-1",
		TemplateID: "template-1",
		Data:       map[string]TemplateData{"first": {Value: "测试消息"}},
	})
	if err != nil {
		t.Fatalf("SendTemplate() 返回错误：%v", err)
	}
	if tokenCalls.Load() != 2 || sendCalls.Load() != 2 {
		t.Fatalf("请求次数不正确：token=%d send=%d", tokenCalls.Load(), sendCalls.Load())
	}
}

func TestExchangeOAuthCodeReturnsOpenID(t *testing.T) {
	client := New(nil)
	client.baseURL = "https://wechat.test"
	client.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/sns/oauth2/access_token" {
			t.Fatalf("未预期的请求路径：%s", request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("appid") != "wx-test" || query.Get("secret") != "secret" || query.Get("code") != "code-1" {
			t.Fatalf("网页授权参数不正确：%s", request.URL.RawQuery)
		}
		return jsonResponse(`{"access_token":"oauth-token","openid":"openid-1","scope":"snsapi_base"}`), nil
	})}

	openID, err := client.ExchangeOAuthCode(t.Context(), Credentials{AppID: "wx-test", AppSecret: "secret"}, "code-1")
	if err != nil {
		t.Fatalf("ExchangeOAuthCode() 返回错误：%v", err)
	}
	if openID != "openid-1" {
		t.Fatalf("OpenID = %q，期望 openid-1", openID)
	}
}
