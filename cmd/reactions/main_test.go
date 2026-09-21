package main

import (
	"context"
	"io"
	"testing"
)

// TestMaintenanceRequiresExplicitOfflineAcknowledgement rejects accidental destructive invocations before connecting.
func TestMaintenanceRequiresExplicitOfflineAcknowledgement(t *testing.T) {
	for _, args := range [][]string{nil, {"import"}, {"restore"}, {"-offline", "unknown"}, {"-offline", "-timeout", "0", "import"}} {
		if err := run(context.Background(), args, io.Discard); err == nil {
			t.Fatalf("accepted unsafe arguments %v", args)
		}
	}
	if err := run(context.Background(), []string{"-h"}, io.Discard); err != nil {
		t.Fatal(err)
	}
}
