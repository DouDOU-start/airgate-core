package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/plugin/hookv2"
)

func TestTransformAccountTestUsesDeclaredV2Capability(t *testing.T) {
	replacement := json.RawMessage(`{"model":"gpt-5.4","stream":true,"input":[{"type":"message"}]}`)
	client := &fakeRelayHookV2Client{handle: func(_ context.Context, request hookv2.Request) (hookv2.Response, error) {
		if request.Path != relayHookV2AccountTestPath || request.Method != http.MethodPost {
			t.Fatalf("request = %s %s", request.Method, request.Path)
		}
		var payload relayHookV2AccountTestRequest
		if err := json.Unmarshal(request.Body, &payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.Mode != "overage" || payload.Platform != "openai" || payload.Endpoint != "responses" || payload.Model != "gpt-5.4" {
			t.Fatalf("payload = %+v", payload)
		}
		return relayHookV2Response(t, relayHookV2AccountTestDecision{
			Version:     relayHookV2Version,
			RequestBody: replacement,
		}), nil
	}}
	manager := NewManager(t.TempDir(), "debug", "", nil)
	manager.instances["account-transform"] = &PluginInstance{
		Name:         "account-transform",
		Capabilities: []string{hookv2.CapabilityAccountTestTransformV1},
		RelayHookV2:  newRelayHookV2Plugin("account-transform", client),
	}
	original := []byte(`{"model":"gpt-5.4","stream":true,"input":"hi"}`)

	got, err := manager.TransformAccountTest(context.Background(), "overage", "openai", "responses", "gpt-5.4", original)
	if err != nil {
		t.Fatalf("TransformAccountTest() error = %v", err)
	}
	if !bytes.Equal(got, replacement) {
		t.Fatalf("body = %s, want %s", got, replacement)
	}
}

func TestTransformAccountTestFailsClosedWithoutHandler(t *testing.T) {
	manager := NewManager(t.TempDir(), "debug", "", nil)
	_, err := manager.TransformAccountTest(
		context.Background(),
		"overage",
		"openai",
		"responses",
		"gpt-5.4",
		[]byte(`{"model":"gpt-5.4","stream":true,"input":"hi"}`),
	)
	if err == nil {
		t.Fatal("TransformAccountTest() unexpectedly succeeded")
	}
}

func TestTransformAccountTestRejectsModelOrStreamMutation(t *testing.T) {
	tests := []struct {
		name        string
		replacement json.RawMessage
	}{
		{name: "model", replacement: json.RawMessage(`{"model":"other","stream":true,"input":"hi"}`)},
		{name: "stream", replacement: json.RawMessage(`{"model":"gpt-5.4","stream":false,"input":"hi"}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeRelayHookV2Client{handle: func(context.Context, hookv2.Request) (hookv2.Response, error) {
				return relayHookV2Response(t, relayHookV2AccountTestDecision{
					Version:     relayHookV2Version,
					RequestBody: tt.replacement,
				}), nil
			}}
			manager := NewManager(t.TempDir(), "debug", "", nil)
			manager.instances["account-transform"] = &PluginInstance{
				Name:         "account-transform",
				Capabilities: []string{hookv2.CapabilityAccountTestTransformV1},
				RelayHookV2:  newRelayHookV2Plugin("account-transform", client),
			}

			_, err := manager.TransformAccountTest(
				context.Background(),
				"overage",
				"openai",
				"responses",
				"gpt-5.4",
				[]byte(`{"model":"gpt-5.4","stream":true,"input":"hi"}`),
			)
			if !errors.Is(err, ErrRelayHookV2AccountTestUnavailable) {
				t.Fatalf("TransformAccountTest() error = %v, want fail-closed", err)
			}
		})
	}
}
