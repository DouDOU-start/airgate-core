package plugin

import "testing"

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
			name:  "responses image tool",
			path:  "/v1/responses",
			model: "gpt-5.4",
			body:  []byte(`{"model":"gpt-5.4","tools":[{"type":"image_generation"}]}`),
			want:  true,
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

			if got := requestNeedsImage(tt.path, tt.model, tt.body); got != tt.want {
				t.Fatalf("requestNeedsImage() = %v, want %v", got, tt.want)
			}
		})
	}
}
