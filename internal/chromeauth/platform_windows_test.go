//go:build windows

package chromeauth

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverAndReadMultipleAccounts(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "Default"), 0700)
	os.WriteFile(filepath.Join(root, "Local State"), []byte(`{"profile":{"info_cache":{"Default":{"name":"Fixture","user_name":"first@example.com"}}}}`), 0600)
	os.WriteFile(filepath.Join(root, "Default", "Preferences"), []byte(`{"account_info":[{"gaia":"111","email":"first@example.com"},{"gaia":"222","email":"second@example.com"},{"gaia":"333","email":"missing@example.com"}],"intl":{"accept_languages":"zh-CN"}}`), 0600)
	db, err := sql.Open("sqlite", filepath.Join(root, "Default", "Web Data"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE token_service(service TEXT PRIMARY KEY, encrypted_token BLOB, binding_key BLOB)"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"111", "222"} {
		if _, err = db.Exec("INSERT INTO token_service VALUES(?,?,?)", "AccountId-"+id, []byte("v20-fixture-"+id), []byte("binding-"+id)); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	accounts, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 {
		t.Fatalf("got %d accounts", len(accounts))
	}
	if !accounts[0].Importable || !accounts[1].Importable || accounts[2].Importable {
		t.Fatal("incorrect per-account eligibility")
	}
	selected, err := selectAccounts(accounts, nil, nil, []string{"Default/222"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].Email != "second@example.com" {
		t.Fatal("selected wrong account")
	}
	gaia, token, binding, err := readTokenService(root, selected[0].Profile, selected[0].GaiaID)
	if err != nil || gaia != "222" || string(token) != "v20-fixture-222" || string(binding) != "binding-222" {
		t.Fatal("credentials do not match selection")
	}
	if _, _, _, err = readTokenService(root, "Default", "999"); err == nil {
		t.Fatal("missing identity must fail, not fall back to another account")
	}
	if _, err = selectAccounts(accounts, nil, nil, []string{"Default/999"}); err == nil {
		t.Fatal("stale selection must fail")
	}
	if _, err = selectAccounts(accounts, nil, nil, []string{"Default/333"}); err == nil {
		t.Fatal("missing credentials must fail")
	}
}
