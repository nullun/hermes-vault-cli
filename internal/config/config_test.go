package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestConfig(t *testing.T, contents string) string {
	t.Helper()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "hermes.conf")
	if err := os.WriteFile(configPath, []byte(contents), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}

func resetGlobals() {
	DbPath = ""
	AlgodURL = ""
	AlgodToken = ""
	IndexerURL = ""
	IndexerToken = ""
	Network = ""
	UserAddress = ""
	Mnemonic = ""
	LogPath = ""
	StatusPath = ""
}

func TestLoadPrefersAlgodUrl(t *testing.T) {
	resetGlobals()

	configPath := writeTestConfig(t, "AlgodUrl = https://mainnet-api.algonode.cloud\n")
	if err := Load(configPath); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if AlgodURL != "https://mainnet-api.algonode.cloud" {
		t.Fatalf("expected AlgodURL from AlgodUrl, got %q", AlgodURL)
	}
}
