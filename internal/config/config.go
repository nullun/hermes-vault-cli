// Package config defines Hermes runtime configuration and protocol constants.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Runtime constants
const (
	WaitRounds      = 30
	CleanupInterval = 10 * time.Minute
	SyncInterval    = 5 * time.Second
	DaemonStaleness = 30 * time.Second

	NumCharsToHighlight = 5
)

// Frontend fee settings (no frontend fee by default)
var FrontendWithdrawalFeeDivisor = uint64(0)

// Config values loaded from config file
var (
	DbPath       string
	AlgodURL     string
	AlgodToken   string
	IndexerURL   string
	IndexerToken string
	Network      string
	UserAddress  string
	Mnemonic     string
	LogPath      string
	StatusPath   string
)

// Load loads config from the given file path.
// Call this explicitly from cmd/root.go rather than using init().
func Load(configPath string) error {
	if configPath == "" {
		configPath = defaultConfigPath()
	}

	// Resolve to absolute path so that DbPath, LogPath, etc. derived via
	// filepath.Dir(configPath) are stable regardless of working directory.
	if configPath != "" {
		if abs, err := filepath.Abs(configPath); err == nil {
			configPath = abs
		}
	}

	// Config file is optional if we can resolve everything from defaults
	if configPath != "" {
		env, err := LoadEnv(configPath)
		if err != nil {
			return fmt.Errorf("failed to load config from %s: %w", configPath, err)
		}
		DbPath = env["DbPath"]
		AlgodURL = env["AlgodUrl"]
		AlgodToken = env["AlgodToken"]
		IndexerURL = env["IndexerUrl"]
		IndexerToken = env["IndexerToken"]
		Network = env["Network"]
		UserAddress = env["UserAddress"]
		Mnemonic = env["Mnemonic"]
		LogPath = env["LogPath"]
	}

	// Default network to mainnet
	if Network == "" {
		Network = "mainnet"
	}

	// Default to free AlgoNode public endpoints when no algod URL is configured.
	// This lets casual users run deposit/withdraw out of the box.
	// The avm package has its own localhost fallback for local development.
	if AlgodURL == "" {
		switch Network {
		case "testnet":
			AlgodURL = "https://testnet-api.algonode.cloud"
		default:
			AlgodURL = "https://mainnet-api.algonode.cloud"
		}
	}

	// Default indexer when algod is a public endpoint and no indexer is configured.
	// Syncing many blocks via algod means one request per round, which is
	// excessive against public endpoints. The indexer can fetch the same
	// data in bulk, so we enable it automatically for the initial catchup.
	if IndexerURL == "" && IsPublicAlgodEndpoint(AlgodURL) {
		switch Network {
		case "testnet":
			IndexerURL = "https://testnet-idx.algonode.cloud"
		default:
			IndexerURL = "https://mainnet-idx.algonode.cloud"
		}
	}

	if DbPath == "" {
		base := "."
		if configPath != "" {
			base = filepath.Dir(configPath)
		}
		DbPath = filepath.Join(base, "hermes.db")
	}

	if LogPath == "" {
		LogPath = filepath.Join(filepath.Dir(DbPath), "hermes.log")
	}

	if StatusPath == "" {
		StatusPath = filepath.Join(filepath.Dir(DbPath), "hermes.status")
	}

	return nil
}

func defaultConfigPath() string {
	if p := os.Getenv("HERMES_CONFIG"); p != "" {
		return p
	}
	// Check local directory first
	if _, err := os.Stat("hermes.conf"); err == nil {
		return "hermes.conf"
	}
	// Check XDG config
	xdg := filepath.Join(os.Getenv("HOME"), ".config/hermes/config")
	if _, err := os.Stat(xdg); err == nil {
		return xdg
	}
	return ""
}

// PublicAlgodEndpoints lists known public/free algod providers.
var PublicAlgodEndpoints = []string{
	"algonode.cloud",
	"nodely.dev",
}

// IsPublicAlgodEndpoint reports whether url belongs to a known public/free provider.
func IsPublicAlgodEndpoint(url string) bool {
	url = strings.ToLower(url)
	for _, endpoint := range PublicAlgodEndpoints {
		if strings.Contains(url, endpoint) {
			return true
		}
	}
	return false
}

// LoadEnv reads key=value pairs from a file, skipping blank lines and comments.
func LoadEnv(filename string) (map[string]string, error) {
	envMap := make(map[string]string)

	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, `"'`)
		envMap[key] = value
	}
	return envMap, scanner.Err()
}
