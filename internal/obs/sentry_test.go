package obs

import (
	"errors"
	"testing"
)

func TestDisabledWithoutDSN(t *testing.T) {
	flush, err := Init(Config{DSN: ""})
	if err != nil {
		t.Fatalf("Init with no DSN should not error: %v", err)
	}
	if Enabled() {
		t.Error("Sentry must be disabled without a DSN")
	}
	// All capture paths must be safe no-ops when disabled.
	Capture(errors.New("boom"), map[string]string{"k": "v"})
	Capture(nil, nil)
	CaptureMessage("hello")
	flush() // must not panic
}
