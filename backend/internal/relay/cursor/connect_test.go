package cursor

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestFrameMessageRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		end     bool
	}{
		{name: "empty non-end", payload: []byte{}, end: false},
		{name: "text non-end", payload: []byte("hello cursor"), end: false},
		{name: "end stream with body", payload: []byte(`{"error":null}`), end: true},
		{name: "binary non-end", payload: []byte{0x00, 0x01, 0xff, 0x7f, 0x80}, end: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frame := FrameMessage(tc.payload, tc.end)
			if len(frame) != frameHeaderLen+len(tc.payload) {
				t.Fatalf("帧长度 = %d, 期望 %d", len(frame), frameHeaderLen+len(tc.payload))
			}
			r := bufio.NewReader(bytes.NewReader(frame))
			end, payload, err := ReadFrame(r)
			if err != nil {
				t.Fatalf("ReadFrame 出错: %v", err)
			}
			if end != tc.end {
				t.Errorf("end = %v, 期望 %v", end, tc.end)
			}
			if !bytes.Equal(payload, tc.payload) {
				t.Errorf("payload = %v, 期望 %v", payload, tc.payload)
			}
		})
	}
}

func TestReadFrameMultipleFrames(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(FrameMessage([]byte("first"), false))
	buf.Write(FrameMessage([]byte("second"), false))
	buf.Write(FrameMessage([]byte(`{"error":null}`), true))

	r := bufio.NewReader(&buf)
	want := []struct {
		payload string
		end     bool
	}{
		{"first", false},
		{"second", false},
		{`{"error":null}`, true},
	}
	for i, w := range want {
		end, payload, err := ReadFrame(r)
		if err != nil {
			t.Fatalf("第 %d 帧读取出错: %v", i, err)
		}
		if end != w.end || string(payload) != w.payload {
			t.Errorf("第 %d 帧 = (%q,%v), 期望 (%q,%v)", i, payload, end, w.payload, w.end)
		}
	}
	if _, _, err := ReadFrame(r); !errors.Is(err, io.EOF) {
		t.Errorf("读尽后应返回 io.EOF, 得到 %v", err)
	}
}

func TestReadFrameTruncatedHeader(t *testing.T) {
	r := bufio.NewReader(bytes.NewReader([]byte{0x00, 0x00}))
	if _, _, err := ReadFrame(r); err == nil {
		t.Fatal("截断头应返回错误")
	}
}

func TestReadFrameTruncatedPayload(t *testing.T) {
	frame := FrameMessage([]byte("full-payload"), false)
	// 只保留头 + 部分载荷
	r := bufio.NewReader(bytes.NewReader(frame[:frameHeaderLen+3]))
	if _, _, err := ReadFrame(r); err == nil {
		t.Fatal("截断载荷应返回错误")
	}
}

func TestEndStreamError(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr bool
		code    string
	}{
		{name: "empty", payload: "", wantErr: false},
		{name: "no error field", payload: `{"metadata":{}}`, wantErr: false},
		{name: "null error", payload: `{"error":null}`, wantErr: false},
		{name: "with error", payload: `{"error":{"code":"resource_exhausted","message":"quota"}}`, wantErr: true, code: "resource_exhausted"},
		{name: "error missing code", payload: `{"error":{"message":"boom"}}`, wantErr: true, code: "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := EndStreamError([]byte(tc.payload))
			if tc.wantErr != (err != nil) {
				t.Fatalf("EndStreamError err=%v, 期望有错=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				var ce *ConnectError
				if !errors.As(err, &ce) {
					t.Fatalf("期望 *ConnectError, 得到 %T", err)
				}
				if ce.Code != tc.code {
					t.Errorf("code = %q, 期望 %q", ce.Code, tc.code)
				}
			}
		})
	}
}
