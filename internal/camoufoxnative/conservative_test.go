//go:build conservative

package camoufoxnative

import (
	"context"
	"testing"
)

func TestConservativeSkipsDriveConsent(t *testing.T) {
	called := false
	err := optionalDriveAuthorization(context.Background(), "fixture@example.invalid", func(context.Context, string) error { called = true; return nil })
	if err != nil || called {
		t.Fatalf("unexpected authorization: called=%v err=%v", called, err)
	}
}
