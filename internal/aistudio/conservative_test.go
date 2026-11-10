//go:build conservative

package aistudio

import "testing"

func TestConservativeAllowsBrowserLoginStateOnlyWithoutRenewalKeys(t *testing.T) {
	state := StorageState{Cookies: []StateCookie{{Name: "fixture", Value: "fixture", Domain: ".google.com", Path: "/"}}}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeState(state); err == nil {
		t.Fatal("copied cookies accepted at runtime")
	}
	state.SetAuthExtension(AuthExtension{Source: AuthSource{Browser: "camoufox", Email: "fixture@example.invalid"}})
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeState(state); err != nil {
		t.Fatal(err)
	}
	state.SetAuthExtension(AuthExtension{Source: AuthSource{Browser: "camoufox"}, OAuth: &ChromeOAuthMaterial{RefreshToken: "fixture"}})
	if err := state.Validate(); err == nil {
		t.Fatal("OAuth material accepted")
	}
	state.SetAuthExtension(AuthExtension{Source: AuthSource{Browser: "camoufox"}, Bound: &BoundSession{ID: "fixture"}})
	if err := state.Validate(); err == nil {
		t.Fatal("bound renewal accepted")
	}
}
