package pipeline

import (
	"bytes"
	"mime/multipart"
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
