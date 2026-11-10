//go:build conservative

package chromeauth

import (
	"context"
	"testing"
)

func TestConservativeRejectsCredentialAccess(t *testing.T) {
	if Supported {
		t.Fatal("Chrome import advertised")
	}
	if _, err := DefaultChromeRoot(); err == nil {
		t.Fatal("profile lookup enabled")
	}
	if _, err := Discover("must-not-be-read"); err == nil {
		t.Fatal("discovery enabled")
	}
	if _, err := retrieveV20Key("must-not-be-read"); err == nil {
		t.Fatal("ABE access enabled")
	}
	if _, err := openDeviceBindingKey([]byte("fixture")); err == nil {
		t.Fatal("device key access enabled")
	}
	if _, err := ReadCDPBrowser(context.Background(), "http://127.0.0.1:1"); err == nil {
		t.Fatal("CDP cookie import enabled")
	}
}
