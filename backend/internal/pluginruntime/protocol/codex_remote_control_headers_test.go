package protocol

import (
	"bytes"
	"testing"
)

func TestCodexExecuteRequestRemoteControlOptionalFieldsRoundTrip(t *testing.T) {
	want := CodexExecuteRequest{
		Version:                      CodexExecutorVersion,
		Endpoint:                     CodexEndpointRemoteControlServerWebSocket,
		Transport:                    CodexTransportWebSocket,
		RemoteControlHostDeviceKind:  "mac_mini",
		RemoteControlSubscribeCursor: "cursor-1",
		Body:                         []byte{0x00, 0xff, 0x10},
	}
	encoded, err := (wireCodec{}).Marshal(&want)
	if err != nil {
		t.Fatal(err)
	}
	var got CodexExecuteRequest
	if err := (wireCodec{}).Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.RemoteControlHostDeviceKind != want.RemoteControlHostDeviceKind || got.RemoteControlSubscribeCursor != want.RemoteControlSubscribeCursor || !bytes.Equal(got.Body, want.Body) {
		t.Fatalf("round-trip request = %+v", got)
	}
}
