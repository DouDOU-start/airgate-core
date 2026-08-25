package hookv2

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

func TestRequestEnvelopeRoundTripPreservesRawBody(t *testing.T) {
	want := Request{
		Method: "POST",
		Path:   "/relay-hook/v1/before-dispatch",
		Query:  "mode=test",
		Header: map[string][]string{"Content-Type": {"application/json"}, "X-Test": {"a", "b"}},
		Body:   []byte{0x00, 0x01, 0x7f, 0x80, 0xff, '{', '}'},
	}
	encoded, err := marshalRequest(want)
	if err != nil {
		t.Fatalf("marshalRequest() error = %v", err)
	}
	var got Request
	if err := unmarshalRequest(encoded, &got); err != nil {
		t.Fatalf("unmarshalRequest() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("request round trip = %#v, want %#v", got, want)
	}
}

func TestResponseEnvelopeRoundTripPreservesRawBody(t *testing.T) {
	want := Response{
		StatusCode: 200,
		Header:     map[string][]string{"Content-Type": {"application/octet-stream"}},
		Body:       []byte{0xff, 0x00, 0xfe, 0x01},
	}
	encoded, err := marshalResponse(want)
	if err != nil {
		t.Fatalf("marshalResponse() error = %v", err)
	}
	var got Response
	if err := unmarshalResponse(encoded, &got); err != nil {
		t.Fatalf("unmarshalResponse() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response round trip = %#v, want %#v", got, want)
	}
}

func TestUnmarshalEnvelopeRejectsCorruptMessages(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "missing length", data: []byte{0x00, 0x01}},
		{name: "length exceeds message", data: appendLengthPrefix(100, []byte("{}"))},
		{name: "invalid metadata json", data: appendLengthPrefix(1, []byte("{"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var metadata requestMetadata
			if _, err := unmarshalEnvelope(tt.data, &metadata); err == nil {
				t.Fatalf("unmarshalEnvelope(%x) unexpectedly succeeded", tt.data)
			}
		})
	}
}

func appendLengthPrefix(length uint32, payload []byte) []byte {
	result := make([]byte, 4, 4+len(payload))
	binary.BigEndian.PutUint32(result, length)
	return append(result, bytes.Clone(payload)...)
}
