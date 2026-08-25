package plugin

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	enttask "github.com/DouDOU-start/airgate-core/ent/task"
)

func TestValidateTaskTransition(t *testing.T) {
	cases := []struct {
		name string
		from enttask.Status
		to   enttask.Status
		code codes.Code
	}{
		{name: "pending to processing", from: enttask.StatusPending, to: enttask.StatusProcessing, code: codes.OK},
		{name: "processing to completed", from: enttask.StatusProcessing, to: enttask.StatusCompleted, code: codes.OK},
		{name: "processing to retrying", from: enttask.StatusProcessing, to: enttask.StatusRetrying, code: codes.OK},
		{name: "retrying to pending", from: enttask.StatusRetrying, to: enttask.StatusPending, code: codes.OK},
		{name: "completed is terminal", from: enttask.StatusCompleted, to: enttask.StatusProcessing, code: codes.FailedPrecondition},
		{name: "pending cannot complete directly", from: enttask.StatusPending, to: enttask.StatusCompleted, code: codes.FailedPrecondition},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTaskTransition(tc.from, tc.to)
			if got := status.Code(err); got != tc.code {
				t.Fatalf("validateTaskTransition() code = %v, want %v", got, tc.code)
			}
		})
	}
}
