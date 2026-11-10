//go:build conservative

package app

import "testing"

func TestConservativeAllowsOnlyQuotaAccountFallback(t *testing.T) {
	if authenticationRecoveryAllowed(0) {
		t.Fatal("authentication recovery enabled")
	}
	if !generationAttemptAllowed(0, false) {
		t.Fatal("first generation disabled")
	}
	if generationAttemptAllowed(1, false) {
		t.Fatal("non-quota automatic generation retry enabled")
	}
	if !generationAttemptAllowed(1, true) {
		t.Fatal("quota account fallback disabled")
	}
}
