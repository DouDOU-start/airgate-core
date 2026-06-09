package plugin

import (
	"testing"

	"github.com/DouDOU-start/airgate-core/internal/scheduler"
)

func TestRequestNeedsImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		path  string
		model string
		body  []byte
		want  bool
	}{
		{
			name:  "chat request",
			path:  "/v1/chat/completions",
			model: "gpt-4o",
			want:  false,
		},
		{
			name:  "image api path",
			path:  "/v1/images/generations",
			model: "gpt-4o",
			want:  true,
		},
		{
			name:  "image model",
			path:  "/v1/responses",
			model: "gpt-image-2",
			want:  true,
		},
		{
			name:  "responses image tool declaration",
			path:  "/v1/responses",
			model: "gpt-5.4",
			body:  []byte(`{"model":"gpt-5.4","tools":[{"type":"image_generation"}]}`),
			want:  false,
		},
		{
			name:  "responses explicit image tool choice string",
			path:  "/v1/responses",
			model: "gpt-5.4",
			body:  []byte(`{"model":"gpt-5.4","tools":[{"type":"image_generation"}],"tool_choice":"image_generation"}`),
			want:  true,
		},
		{
			name:  "responses explicit image tool choice object",
			path:  "/v1/responses",
			model: "gpt-5.4",
			body:  []byte(`{"model":"gpt-5.4","tools":[{"type":"image_generation"}],"tool_choice":{"type":"image_generation"}}`),
			want:  true,
		},
		{
			name:  "responses required with only image tool",
			path:  "/v1/responses",
			model: "gpt-5.4",
			body:  []byte(`{"model":"gpt-5.4","tools":[{"type":"image_generation"}],"tool_choice":"required"}`),
			want:  true,
		},
		{
			name:  "responses required with mixed tools",
			path:  "/v1/responses",
			model: "gpt-5.4",
			body:  []byte(`{"model":"gpt-5.4","tools":[{"type":"image_generation"},{"type":"web_search"}],"tool_choice":"required"}`),
			want:  false,
		},
		{
			name:  "responses other tool",
			path:  "/v1/responses",
			model: "gpt-5.4",
			body:  []byte(`{"model":"gpt-5.4","tools":[{"type":"web_search"}]}`),
			want:  false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := requestNeedsImage(nil, tt.path, tt.model, tt.body); got != tt.want {
				t.Fatalf("requestNeedsImage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccountRequirementsForRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		path          string
		model         string
		body          []byte
		wantWorkload  scheduler.Workload
		wantProtocols []scheduler.ImageProtocol
	}{
		{
			name:         "chat request",
			path:         "/v1/chat/completions",
			model:        "gpt-4o",
			wantWorkload: scheduler.WorkloadChat,
		},
		{
			name:          "images api accepts either image protocol",
			path:          "/v1/images/generations",
			model:         "gpt-image-2",
			wantWorkload:  scheduler.WorkloadImage,
			wantProtocols: []scheduler.ImageProtocol{scheduler.ImageProtocolImagesAPI, scheduler.ImageProtocolResponsesTool},
		},
		{
			name:          "forced image tool requires responses tool",
			path:          "/v1/responses",
			model:         "gpt-5.4",
			body:          []byte(`{"model":"gpt-5.4","tools":[{"type":"image_generation"}],"tool_choice":"image_generation"}`),
			wantWorkload:  scheduler.WorkloadImage,
			wantProtocols: []scheduler.ImageProtocol{scheduler.ImageProtocolResponsesTool},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := accountRequirementsForRequest(nil, tt.path, tt.model, tt.body)
			if got.Workload != tt.wantWorkload {
				t.Fatalf("Workload = %q, want %q", got.Workload, tt.wantWorkload)
			}
			if !sameImageProtocols(got.ImageProtocols, tt.wantProtocols) {
				t.Fatalf("ImageProtocols = %v, want %v", got.ImageProtocols, tt.wantProtocols)
			}
		})
	}
}

func sameImageProtocols(a, b []scheduler.ImageProtocol) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
