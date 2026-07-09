package pipeline

import (
	"bytes"
	"mime/multipart"
	"strings"
	"testing"
)

// buildMultipart 构造 multipart/form-data 体：values 为普通字段，files 为文件 part。
// 返回 (body, contentType)。
func buildMultipart(t *testing.T, values map[string]string, files map[string][]byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, v := range values {
		if err := w.WriteField(name, v); err != nil {
			t.Fatalf("WriteField(%q): %v", name, err)
		}
	}
	for name, content := range files {
		fw, err := w.CreateFormFile(name, name+".png")
		if err != nil {
			t.Fatalf("CreateFormFile(%q): %v", name, err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatalf("写文件 part: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes(), w.FormDataContentType()
}

// TestExtractMultipartFields multipart 字段提取表驱动：只取指定普通字段，
// 文件 part（含与目标同名的文件）一律跳过，原始体不被消费改动。
func TestExtractMultipartFields(t *testing.T) {
	t.Run("提取 model 与 stream，跳过文件 part", func(t *testing.T) {
		body, ct := buildMultipart(t,
			map[string]string{"model": "gpt-image-1", "stream": "true", "prompt": "a cat"},
			map[string][]byte{"image": []byte("PNGDATA")},
		)
		got, err := extractMultipartFields(body, ct, "model", "stream")
		if err != nil {
			t.Fatalf("extractMultipartFields: %v", err)
		}
		if got["model"] != "gpt-image-1" || got["stream"] != "true" {
			t.Errorf("fields = %v, want model=gpt-image-1 stream=true", got)
		}
		if _, ok := got["prompt"]; ok {
			t.Error("不应提取未指定字段 prompt")
		}
	})

	t.Run("缺 model 字段返回空值", func(t *testing.T) {
		body, ct := buildMultipart(t, map[string]string{"prompt": "x"}, map[string][]byte{"image": []byte("D")})
		got, err := extractMultipartFields(body, ct, "model")
		if err != nil {
			t.Fatalf("extractMultipartFields: %v", err)
		}
		if got["model"] != "" {
			t.Errorf("model = %q, want 空", got["model"])
		}
	})

	t.Run("与目标同名的文件 part 不当作字段值", func(t *testing.T) {
		body, ct := buildMultipart(t, nil, map[string][]byte{"model": []byte("FILEDATA")})
		got, err := extractMultipartFields(body, ct, "model")
		if err != nil {
			t.Fatalf("extractMultipartFields: %v", err)
		}
		if got["model"] != "" {
			t.Errorf("model = %q, want 空（文件 part 不算字段）", got["model"])
		}
	})

	t.Run("非 multipart Content-Type 报错", func(t *testing.T) {
		if _, err := extractMultipartFields([]byte(`{"model":"x"}`), "application/json", "model"); err == nil {
			t.Error("application/json 应报错")
		}
	})

	t.Run("缺 boundary 报错", func(t *testing.T) {
		if _, err := extractMultipartFields([]byte("x"), "multipart/form-data", "model"); err == nil {
			t.Error("缺 boundary 应报错")
		}
	})

	t.Run("超长字段值被截断而非报错", func(t *testing.T) {
		long := strings.Repeat("a", maxMultipartFieldBytes*2)
		body, ct := buildMultipart(t, map[string]string{"model": long}, nil)
		got, err := extractMultipartFields(body, ct, "model")
		if err != nil {
			t.Fatalf("extractMultipartFields: %v", err)
		}
		if len(got["model"]) != maxMultipartFieldBytes {
			t.Errorf("len(model) = %d, want 截断到 %d", len(got["model"]), maxMultipartFieldBytes)
		}
	})

	t.Run("提取不消费原始体（可重复提取）", func(t *testing.T) {
		body, ct := buildMultipart(t, map[string]string{"model": "m1"}, map[string][]byte{"image": []byte("D")})
		before := append([]byte(nil), body...)
		if _, err := extractMultipartFields(body, ct, "model"); err != nil {
			t.Fatalf("第一次提取: %v", err)
		}
		if !bytes.Equal(body, before) {
			t.Fatal("原始体被改动")
		}
		got, err := extractMultipartFields(body, ct, "model")
		if err != nil || got["model"] != "m1" {
			t.Errorf("第二次提取 = %v, %v", got, err)
		}
	})
}
