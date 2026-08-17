package protocol

import (
	"bytes"
	"testing"
)

func TestWireCodecRequest正文原样往返(t *testing.T) {
	want := Request{
		Method: "POST",
		Path:   "/relay-hook/v1/before-dispatch",
		Header: map[string][]string{"Content-Type": {"application/json"}},
		Body:   []byte(`{"input":[{"type":"message","content":"你好"}]}`),
	}
	codec := wireCodec{}
	encoded, err := codec.Marshal(&want)
	if err != nil {
		t.Fatalf("编码请求失败: %v", err)
	}
	if len(encoded)-len(want.Body) > 512 {
		t.Fatalf("请求消息元数据开销异常: body=%d encoded=%d", len(want.Body), len(encoded))
	}
	var got Request
	if err := codec.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("解码请求失败: %v", err)
	}
	if got.Method != want.Method || got.Path != want.Path || !bytes.Equal(got.Body, want.Body) {
		t.Fatalf("请求往返结果不一致: %+v", got)
	}
}

func TestWireCodecResponse二进制正文原样往返(t *testing.T) {
	want := Response{StatusCode: 200, Body: []byte{0x00, 0xff, 0x10}}
	codec := wireCodec{}
	encoded, err := codec.Marshal(&want)
	if err != nil {
		t.Fatalf("编码响应失败: %v", err)
	}
	var got Response
	if err := codec.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("解码响应失败: %v", err)
	}
	if got.StatusCode != want.StatusCode || !bytes.Equal(got.Body, want.Body) {
		t.Fatalf("响应往返结果不一致: %+v", got)
	}
}

func TestWireCodec拒绝损坏消息(t *testing.T) {
	var request Request
	if err := (wireCodec{}).Unmarshal([]byte{0, 0, 0, 8, '{', '}'}, &request); err == nil {
		t.Fatal("应拒绝无效元数据长度")
	}
}
