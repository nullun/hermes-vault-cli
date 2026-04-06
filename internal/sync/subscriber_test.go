package sync

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nullun/hermes-vault-cli/internal/db"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/indexer"
)

func openSubscriberTestDB(t *testing.T) {
	t.Helper()

	db.Close()
	dbPath := filepath.Join(t.TempDir(), "hermes.db")
	if err := db.Open(dbPath); err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(db.Close)
}

func TestSyncToTipAdvancesWatermarkOnlyToLastSuccessfulRound(t *testing.T) {
	openSubscriberTestDB(t)

	if err := db.SetWatermark(5); err != nil {
		t.Fatalf("db.SetWatermark() error = %v", err)
	}

	expectedErr := errors.New("boom")
	s := &Subscriber{
		indexer: &indexer.Client{},
		statusFn: func(context.Context) (uint64, error) {
			return 2006, nil
		},
		allowIndexerFallback: true,
		syncWithIndexerFn: func(context.Context, uint64, uint64) (uint64, uint64, error) {
			return 3, 7, expectedErr
		},
	}

	_, err := s.SyncToTip(context.Background())
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

	watermark, err := db.GetWatermark()
	if err != nil {
		t.Fatalf("db.GetWatermark() error = %v", err)
	}
	if watermark != 7 {
		t.Fatalf("expected watermark 7, got %d", watermark)
	}
}

func TestSyncToTipAdvancesWatermarkToTipOnSuccess(t *testing.T) {
	openSubscriberTestDB(t)

	s := &Subscriber{
		statusFn: func(context.Context) (uint64, error) {
			return 12, nil
		},
		syncWithAlgodFn: func(context.Context, uint64, uint64) (uint64, uint64, error) {
			return 4, 12, nil
		},
	}

	processed, err := s.SyncToTip(context.Background())
	if err != nil {
		t.Fatalf("SyncToTip() error = %v", err)
	}
	if processed != 4 {
		t.Fatalf("expected 4 processed txns, got %d", processed)
	}

	watermark, err := db.GetWatermark()
	if err != nil {
		t.Fatalf("db.GetWatermark() error = %v", err)
	}
	if watermark != 12 {
		t.Fatalf("expected watermark 12, got %d", watermark)
	}
}

func TestSyncToTipPrefersAlgodWhenGapWithinHistoryWindow(t *testing.T) {
	openSubscriberTestDB(t)

	if err := db.SetWatermark(25); err != nil {
		t.Fatalf("db.SetWatermark() error = %v", err)
	}

	algodCalled := false
	indexerCalled := false
	s := &Subscriber{
		indexer: &indexer.Client{},
		statusFn: func(context.Context) (uint64, error) {
			return 100, nil
		},
		syncWithAlgodFn: func(context.Context, uint64, uint64) (uint64, uint64, error) {
			algodCalled = true
			return 0, 100, nil
		},
		syncWithIndexerFn: func(context.Context, uint64, uint64) (uint64, uint64, error) {
			indexerCalled = true
			return 0, 100, nil
		},
	}

	if _, err := s.SyncToTip(context.Background()); err != nil {
		t.Fatalf("SyncToTip() error = %v", err)
	}
	if !algodCalled {
		t.Fatalf("expected algod sync to be called")
	}
	if indexerCalled {
		t.Fatalf("expected indexer sync not to be called")
	}
}

func TestSyncToTipConfirmsBeforeIndexerFallback(t *testing.T) {
	openSubscriberTestDB(t)

	if err := db.SetWatermark(1); err != nil {
		t.Fatalf("db.SetWatermark() error = %v", err)
	}

	confirmed := false
	indexerCalled := false
	s := &Subscriber{
		indexer: &indexer.Client{},
		statusFn: func(context.Context) (uint64, error) {
			return 1505, nil
		},
		confirmIndexerFallbackFn: func(rounds uint64) (bool, error) {
			confirmed = true
			if rounds != 1504 {
				t.Fatalf("expected 1504 rounds behind, got %d", rounds)
			}
			return true, nil
		},
		syncWithIndexerFn: func(context.Context, uint64, uint64) (uint64, uint64, error) {
			indexerCalled = true
			return 0, 1505, nil
		},
	}

	if _, err := s.SyncToTip(context.Background()); err != nil {
		t.Fatalf("SyncToTip() error = %v", err)
	}
	if !confirmed {
		t.Fatalf("expected fallback confirmation to be requested")
	}
	if !indexerCalled {
		t.Fatalf("expected indexer sync to be called after confirmation")
	}
}

func TestSyncToTipRequiresApprovalForIndexerFallback(t *testing.T) {
	openSubscriberTestDB(t)

	s := &Subscriber{
		indexer: &indexer.Client{},
		statusFn: func(context.Context) (uint64, error) {
			return 1500, nil
		},
	}

	if _, err := s.SyncToTip(context.Background()); err == nil {
		t.Fatalf("expected error when indexer fallback is not approved")
	}
}

func TestPublicAlgodEndpointRequiresIndexer(t *testing.T) {
	openSubscriberTestDB(t)

	// Public endpoint without indexer should fail
	s := &Subscriber{
		algodURL: "https://mainnet-api.algonode.cloud",
		statusFn: func(context.Context) (uint64, error) {
			return 1500, nil
		},
	}

	_, err := s.SyncToTip(context.Background())
	if err == nil {
		t.Fatalf("expected error when using public algod endpoint without indexer")
	}
	if !contains(err.Error(), "cannot sync") {
		t.Fatalf("expected 'cannot sync' error, got: %v", err)
	}
}

func TestPublicAlgodEndpointWithIndexerUsesIndexer(t *testing.T) {
	openSubscriberTestDB(t)

	// Public endpoint with indexer should automatically use indexer
	s := &Subscriber{
		algodURL: "https://mainnet-api.algonode.cloud",
		indexer:  &indexer.Client{},
		statusFn: func(context.Context) (uint64, error) {
			return 1500, nil
		},
		syncWithIndexerFn: func(context.Context, uint64, uint64) (uint64, uint64, error) {
			return 0, 1500, nil
		},
	}

	_, err := s.SyncToTip(context.Background())
	if err != nil {
		t.Fatalf("expected no error with indexer configured, got: %v", err)
	}
}

func TestIsPublicAlgodEndpoint(t *testing.T) {
	tests := []struct {
		url      string
		isPublic bool
	}{
		{"https://mainnet-api.algonode.cloud", true},
		{"https://testnet-api.algonode.cloud", true},
		{"https://mainnet-api.nodely.dev", true},
		{"http://localhost:4001", false},
		{"https://my-private-algod.example.com", false},
		{"HTTPS://MAINNET-API.ALGONODE.CLOUD", true}, // case insensitive
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := isPublicAlgodEndpoint(tt.url); got != tt.isPublic {
				t.Fatalf("isPublicAlgodEndpoint(%q) = %v, want %v", tt.url, got, tt.isPublic)
			}
		})
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
