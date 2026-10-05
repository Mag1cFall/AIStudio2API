package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaFallbackConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	cfg := Default()
	if cfg.SchemaFallback {
		t.Fatal("lossy fallback must be opt-in")
	}
	for _, enabled := range []bool{true, false} {
		cfg.SchemaFallback = enabled
		if err := cfg.Save(path); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(path)
		if err != nil || loaded.SchemaFallback != enabled {
			t.Fatalf("env round trip: %v", err)
		}
		raw, err := json.Marshal(loaded)
		if err != nil {
			t.Fatal(err)
		}
		var restored Config
		if err := json.Unmarshal(raw, &restored); err != nil || restored.SchemaFallback != enabled {
			t.Fatalf("JSON round trip: %v", err)
		}
	}
	t.Setenv("PLAYGROUND_SCHEMA_FALLBACK", "true")
	loaded, err := Load(path)
	if err != nil || !loaded.SchemaFallback {
		t.Fatalf("environment override: %v", err)
	}
	t.Setenv("PLAYGROUND_SCHEMA_FALLBACK", "invalid")
	if _, err := Load(path); err == nil {
		t.Fatal("invalid boolean accepted")
	}
	if err := os.Unsetenv("PLAYGROUND_SCHEMA_FALLBACK"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("UPSTREAM_CHANNELS=playground\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path)
	if err != nil || loaded.SchemaFallback {
		t.Fatalf("old env must remain strict: %v", err)
	}
}
