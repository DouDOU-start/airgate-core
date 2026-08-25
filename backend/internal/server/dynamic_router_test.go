package server

import (
	"net/http"
	"testing"
)

func TestIsWebSocketUpgradeRequiresUpgradeToken(t *testing.T) {
	cases := []struct {
		name       string
		upgrade    string
		connection string
		want       bool
	}{
		{name: "standard", upgrade: "websocket", connection: "Upgrade", want: true},
		{name: "case insensitive", upgrade: "WebSocket", connection: "keep-alive, Upgrade", want: true},
		{name: "missing connection token", upgrade: "websocket", connection: "keep-alive", want: false},
		{name: "wrong upgrade", upgrade: "h2c", connection: "Upgrade", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "http://gateway.example/v1/responses", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Upgrade", tc.upgrade)
			req.Header.Set("Connection", tc.connection)
			if got := isWebSocketUpgrade(req); got != tc.want {
				t.Fatalf("isWebSocketUpgrade() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDynamicRouteMatchesSyntheticWebSocketMethod(t *testing.T) {
	routes := map[string]bool{
		"WS /v1/responses":          true,
		"POST /v1/chat/completions": true,
	}
	if !dynamicRouteMatches(routes, http.MethodGet, "/v1/responses", true) {
		t.Fatal("WS declaration did not match a GET upgrade")
	}
	if dynamicRouteMatches(routes, http.MethodGet, "/v1/chat/completions", true) {
		t.Fatal("ordinary POST route matched an unrelated GET upgrade")
	}
	if !dynamicRouteMatches(routes, http.MethodPost, "/v1/chat/completions", false) {
		t.Fatal("ordinary HTTP route did not match its method")
	}
}
