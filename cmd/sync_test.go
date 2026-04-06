package cmd

import (
	"context"
	"testing"

	"github.com/nullun/hermes-vault-cli/internal/sync"
)

func TestRunSyncInitializesAppAndSubscriber(t *testing.T) {
	originalEnsure := ensureRuntimeFn
	originalSync := subscriberSyncToTipFn
	defer func() {
		ensureRuntimeFn = originalEnsure
		subscriberSyncToTipFn = originalSync
	}()

	ensureCalled := false
	syncCalled := false

	ensureRuntimeFn = func() error {
		ensureCalled = true
		return nil
	}
	subscriberSyncToTipFn = func(ctx context.Context) (uint64, error) {
		syncCalled = true
		return 0, nil
	}

	if err := runSync(nil, nil); err != nil {
		t.Fatalf("runSync returned error: %v", err)
	}
	if !ensureCalled {
		t.Fatalf("expected ensureRuntime to be called")
	}
	if !syncCalled {
		t.Fatalf("expected subscriber sync to be called")
	}
}

func TestSyncForceIndexerFlagSetsSubscriberOption(t *testing.T) {
	originalEnsure := ensureRuntimeFn
	originalSync := subscriberSyncToTipFn
	originalForceIndexer := syncForceIndexer
	defer func() {
		ensureRuntimeFn = originalEnsure
		subscriberSyncToTipFn = originalSync
		syncForceIndexer = originalForceIndexer
	}()

	mockSubscriber := &sync.Subscriber{}
	mockRT := &runtime{
		subscriber: mockSubscriber,
	}

	ensureRuntimeFn = func() error {
		rt = mockRT
		return nil
	}
	subscriberSyncToTipFn = func(ctx context.Context) (uint64, error) {
		return 0, nil
	}

	syncForceIndexer = true
	if err := runSync(nil, nil); err != nil {
		t.Fatalf("runSync returned error: %v", err)
	}

	if !mockSubscriber.ForceIndexerEnabled() {
		t.Fatalf("expected force indexer to be set on subscriber")
	}
}
