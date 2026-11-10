//go:build conservative

package app

import "testing"

func TestConservativeDoesNotRecoverOrRegenerate(t *testing.T) {
	if authenticationRecoveryAllowed(0) {
		t.Fatal("authentication recovery enabled")
	}
	if !generationAttemptAllowed(0) {
		t.Fatal("first generation disabled")
	}
	if generationAttemptAllowed(1) {
		t.Fatal("automatic generation retry enabled")
	}
}
