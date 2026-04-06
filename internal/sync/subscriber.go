// Package sync implements blockchain event monitoring.
package sync

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"time"

	"github.com/nullun/hermes-vault-cli/internal/config"
	"github.com/nullun/hermes-vault-cli/internal/db"
	"github.com/nullun/hermes-vault-cli/internal/logger"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/algod"
	indexerModels "github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/indexer"
	"github.com/algorand/go-algorand-sdk/v2/crypto"
	"github.com/algorand/go-algorand-sdk/v2/types"
)

// Subscriber polls the Algorand blockchain and saves relevant transactions to the database.
type Subscriber struct {
	algod                    *algod.Client
	algodURL                 string
	indexer                  *indexer.Client // nil if not configured
	allowIndexerFallback     bool
	forceIndexer             bool
	appID                    uint64
	appCreationBlock         uint64
	depositSelector          []byte
	withdrawSelector         []byte
	statusFn                 func(context.Context) (uint64, error)
	confirmIndexerFallbackFn func(uint64) (bool, error)
	syncWithIndexerFn        func(context.Context, uint64, uint64) (uint64, uint64, error)
	syncWithAlgodFn          func(context.Context, uint64, uint64) (uint64, uint64, error)
}

const algodHistoryLookbackRounds uint64 = 1000
const publicAlgodHistoryLookbackRounds uint64 = 10

// isPublicAlgodEndpoint delegates to config.IsPublicAlgodEndpoint.
func isPublicAlgodEndpoint(url string) bool {
	return config.IsPublicAlgodEndpoint(url)
}

// New creates a Subscriber. indexerClient may be nil.
func New(algodClient *algod.Client, indexerClient *indexer.Client,
	appID uint64, appCreationBlock uint64,
	depositSelector, withdrawSelector []byte,
	algodURL string,
) *Subscriber {
	s := &Subscriber{
		algod:            algodClient,
		algodURL:         algodURL,
		indexer:          indexerClient,
		appID:            appID,
		appCreationBlock: appCreationBlock,
		depositSelector:  depositSelector,
		withdrawSelector: withdrawSelector,
	}
	s.statusFn = s.chainStatus
	s.syncWithIndexerFn = s.syncWithIndexer
	s.syncWithAlgodFn = s.syncWithAlgod
	return s
}

func (s *Subscriber) SetAllowIndexerFallback(allow bool) {
	s.allowIndexerFallback = allow
}

func (s *Subscriber) SetForceIndexer(force bool) {
	s.forceIndexer = force
}

func (s *Subscriber) ForceIndexerEnabled() bool {
	return s.forceIndexer
}

func (s *Subscriber) SetConfirmIndexerFallbackFn(fn func(uint64) (bool, error)) {
	s.confirmIndexerFallbackFn = fn
}

func (s *Subscriber) NeedsIndexerFallback(ctx context.Context) (bool, uint64, error) {
	currentRound, err := s.statusFn(ctx)
	if err != nil {
		return false, 0, err
	}

	watermark, err := db.GetWatermark()
	if err != nil {
		return false, 0, fmt.Errorf("failed to get watermark: %w", err)
	}

	if watermark == 0 && s.appCreationBlock > 0 {
		watermark = s.appCreationBlock
	}

	if currentRound <= watermark {
		return false, 0, nil
	}

	blocksToProcess := currentRound - watermark
	return blocksToProcess > algodHistoryLookbackRounds, blocksToProcess, nil
}

func (s *Subscriber) HasIndexer() bool {
	return s.indexer != nil
}

func (s *Subscriber) Algod() *algod.Client {
	return s.algod
}

func (s *Subscriber) AppID() uint64 {
	return s.appID
}

func (s *Subscriber) CreationBlock() uint64 {
	return s.appCreationBlock
}

func (s *Subscriber) chainStatus(ctx context.Context) (uint64, error) {
	status, err := s.algod.Status().Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get algod status: %w", err)
	}
	return status.LastRound, nil
}

// SyncToTip processes all unprocessed blocks up to the current chain tip.
// Returns the number of relevant transactions processed.
func (s *Subscriber) SyncToTip(ctx context.Context) (uint64, error) {
	currentRound, err := s.statusFn(ctx)
	if err != nil {
		return 0, err
	}

	watermark, err := db.GetWatermark()
	if err != nil {
		return 0, fmt.Errorf("failed to get watermark: %w", err)
	}

	// Ensure watermark is at least the app creation block
	if watermark == 0 && s.appCreationBlock > 0 {
		watermark = s.appCreationBlock
		if err := db.SetWatermark(watermark); err != nil {
			return 0, err
		}
	}

	if currentRound <= watermark {
		return 0, nil
	}

	blocksToProcess := currentRound - watermark
	if blocksToProcess > 10 {
		fmt.Fprintf(os.Stderr, "Syncing %d blocks (round %d → %d)...\n", blocksToProcess, watermark, currentRound)
	}

	var processed uint64
	var lastSuccessfulRound uint64
	useIndexer, err := s.shouldUseIndexerFallback(blocksToProcess)
	if err != nil {
		return 0, err
	}
	if useIndexer {
		if s.forceIndexer {
			logger.Log("Force-indexer enabled; using indexer for sync (rounds %d → %d).", watermark+1, currentRound)
		} else {
			logger.Log("Algod history gap is %d rounds; using indexer fallback.", blocksToProcess)
		}
		processed, lastSuccessfulRound, err = s.syncWithIndexerFn(ctx, watermark+1, currentRound)
	} else {
		// If the gap is large and we have no indexer, check if algod actually has the blocks
		// before we start a potentially doomed sync.
		if blocksToProcess > algodHistoryLookbackRounds {
			nextRound := watermark + 1
			if _, err := s.algod.Block(nextRound).Do(ctx); err != nil {
				return 0, fmt.Errorf("algod is %d rounds behind and no indexer is configured. The node is missing block %d and cannot sync this much history", blocksToProcess, nextRound)
			}
		}
		processed, lastSuccessfulRound, err = s.syncWithAlgodFn(ctx, watermark+1, currentRound)
	}
	if lastSuccessfulRound > watermark {
		if setErr := db.SetWatermark(lastSuccessfulRound); setErr != nil {
			return processed, setErr
		}
	}
	if err != nil {
		return processed, err
	}
	if blocksToProcess > 10 {
		fmt.Fprintf(os.Stderr, "Sync complete. Processed %d relevant transactions.\n", processed)
	}
	return processed, nil
}

func (s *Subscriber) shouldUseIndexerFallback(blocksToProcess uint64) (bool, error) {
	// Check if using a public algod endpoint - enforce stricter limits
	if isPublicAlgodEndpoint(s.algodURL) {
		if blocksToProcess > publicAlgodHistoryLookbackRounds {
			if s.indexer == nil {
				return false, fmt.Errorf(
					"cannot sync %d blocks from public algod endpoint (%s) without an indexer. "+
						"Syncing block-by-block would make too many requests against a public endpoint. "+
						"Please configure an indexer in your config file or use --allow-indexer if an indexer is available",
					blocksToProcess, s.algodURL)
			}
			logger.Log("Using indexer for sync: public algod endpoint (%s) with %d block gap (limit: %d)", s.algodURL, blocksToProcess, publicAlgodHistoryLookbackRounds)
			return true, nil
		}
		// Public endpoint but within limits - use algod
		return false, nil
	}

	if s.indexer == nil {
		return false, nil
	}
	if s.forceIndexer {
		return true, nil
	}
	if blocksToProcess <= algodHistoryLookbackRounds {
		return false, nil
	}
	if s.allowIndexerFallback {
		return true, nil
	}
	if s.confirmIndexerFallbackFn == nil {
		return false, fmt.Errorf("algod history gap is %d rounds; rerun with --allow-indexer to use the configured indexer", blocksToProcess)
	}
	ok, err := s.confirmIndexerFallbackFn(blocksToProcess)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("algod history gap is %d rounds and indexer fallback was declined", blocksToProcess)
	}
	return true, nil
}

// Run starts a background goroutine that periodically syncs to the chain tip.
func (s *Subscriber) Run(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(config.SyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.SyncToTip(ctx); err != nil {
					logger.Log("subscriber sync error: %v", err)
				}
			}
		}
	}()
}

// ────────────────────────────────────────────────────────────────
// Indexer-based sync (fallback when algod history is unavailable)
// ────────────────────────────────────────────────────────────────

func (s *Subscriber) syncWithIndexer(ctx context.Context, fromRound, toRound uint64,
) (uint64, uint64, error) {
	var processed uint64
	var nextToken string
	safeRound := fromRound - 1
	pendingRound := uint64(0)
	pageNum := 0

	logger.Log("Starting indexer sync: app ID %d, rounds %d→%d", s.appID, fromRound, toRound)

	for {
		pageNum++
		req := s.indexer.SearchForTransactions().
			ApplicationId(s.appID).
			MinRound(fromRound).
			MaxRound(toRound)

		if nextToken != "" {
			req = req.NextToken(nextToken)
		}

		logger.Log("Fetching page %d from indexer...", pageNum)
		result, err := req.Do(ctx)
		if err != nil {
			return processed, safeRound, fmt.Errorf("indexer search error: %w", err)
		}

		txnCount := len(result.Transactions)
		logger.Log("Page %d: received %d transaction(s)", pageNum, txnCount)

		for i := range result.Transactions {
			txn := result.Transactions[i]
			if pendingRound == 0 {
				pendingRound = txn.ConfirmedRound
			} else if txn.ConfirmedRound != pendingRound {
				safeRound = pendingRound
				pendingRound = txn.ConfirmedRound
			}

			if err := s.processIndexerTxn(txn); err != nil {
				return processed, safeRound, fmt.Errorf("error processing txn %s: %w", txn.Id, err)
			}
			processed++
		}

		nextToken = result.NextToken
		if nextToken == "" {
			logger.Log("Indexer sync complete: %d page(s), %d transaction(s) processed", pageNum, processed)
			break
		}
		logger.Log("More results available; fetching next page...")
	}
	if pendingRound != 0 {
		safeRound = pendingRound
	} else {
		safeRound = toRound
	}
	return processed, safeRound, nil
}

func (s *Subscriber) processIndexerTxn(txn indexerModels.Transaction) error {
	appTxn := txn.ApplicationTransaction
	if appTxn.ApplicationId != s.appID {
		return nil
	}
	if len(appTxn.ApplicationArgs) == 0 {
		return nil
	}
	firstArg := appTxn.ApplicationArgs[0]
	if len(firstArg) < 4 {
		return nil
	}

	// Last log entry is the ARC-4 return value
	if len(txn.Logs) == 0 {
		return nil
	}
	lastLog := txn.Logs[len(txn.Logs)-1]
	leafIndex, treeRoot, err := parseTxnLog(lastLog)
	if err != nil {
		return fmt.Errorf("parse txn log: %w", err)
	}

	txnID := txn.Id
	block := txn.ConfirmedRound
	selector := firstArg[:4]
	args := appTxn.ApplicationArgs
	accounts := appTxn.Accounts

	switch {
	case equalSelector(selector, s.depositSelector):
		return s.handleDeposit(args, leafIndex, treeRoot, txnID, block)
	case equalSelector(selector, s.withdrawSelector):
		return s.handleWithdrawal(args, accounts, leafIndex, treeRoot, txnID, block)
	}
	return nil
}

// ────────────────────────────────────────────────────────────────
// Algod block-by-block sync (primary path)
// ────────────────────────────────────────────────────────────────

func (s *Subscriber) syncWithAlgod(ctx context.Context, fromRound, toRound uint64,
) (uint64, uint64, error) {
	var processed uint64
	showProgress := (toRound - fromRound) > 100

	for round := fromRound; round <= toRound; round++ {
		select {
		case <-ctx.Done():
			return processed, round - 1, ctx.Err()
		default:
		}

		if showProgress && round%1000 == 0 {
			pct := float64(round-fromRound) / float64(toRound-fromRound) * 100
			fmt.Fprintf(os.Stderr, "  %.1f%% (round %d/%d)\n", pct, round, toRound)
		}

		n, err := s.processBlock(ctx, round)
		if err != nil {
			return processed, round - 1, fmt.Errorf("error processing block %d: %w", round, err)
		}
		processed += uint64(n)

		// Checkpoint watermark periodically so progress survives restarts
		if round%500 == 0 {
			if err := db.SetWatermark(round); err != nil {
				return processed, round, fmt.Errorf("checkpoint watermark at round %d: %w", round, err)
			}
		}
	}
	return processed, toRound, nil
}

func (s *Subscriber) processBlock(ctx context.Context, round uint64) (int, error) {
	block, err := s.algod.Block(round).Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get block %d: %w", round, err)
	}

	count := 0
	for i := range block.Payset {
		stib := &block.Payset[i]
		txn := stib.SignedTxn.Txn

		// Only process application calls to our app
		if txn.Type != types.ApplicationCallTx {
			continue
		}
		if uint64(txn.ApplicationID) != s.appID {
			continue
		}
		if len(txn.ApplicationArgs) == 0 {
			continue
		}

		selector := txn.ApplicationArgs[0]
		if len(selector) < 4 {
			continue
		}

		// Logs are in the ApplyData
		logs := stib.ApplyData.EvalDelta.Logs
		if len(logs) == 0 {
			continue
		}
		// Logs in types.EvalDelta are []string of raw bytes
		lastLogBytes := []byte(logs[len(logs)-1])
		leafIndex, treeRoot, err := parseTxnLog(lastLogBytes)
		if err != nil {
			continue
		}

		txnID := crypto.GetTxID(txn)
		args := txn.ApplicationArgs

		switch {
		case equalSelector(selector, s.depositSelector):
			// For algod blocks we don't have decoded accounts; address parsing is limited
			if err := s.handleDeposit(args, leafIndex, treeRoot, txnID, round); err != nil {
				logger.Log("handle deposit block %d: %v", round, err)
			} else {
				count++
			}
		case equalSelector(selector, s.withdrawSelector):
			// ForeignAccounts are not directly available from types.Transaction
			// Use empty slice; address will be empty for algod-only path
			if err := s.handleWithdrawal(args, nil, leafIndex, treeRoot, txnID, round); err != nil {
				logger.Log("handle withdrawal block %d: %v", round, err)
			} else {
				count++
			}
		}
	}
	return count, nil
}

// ────────────────────────────────────────────────────────────────
// Transaction handlers
// ────────────────────────────────────────────────────────────────

// handleDeposit extracts deposit data from transaction arguments and saves to db.
//
// ARC-4 deposit args:
//
//	args[0] = method selector
//	args[1] = ZK proof (byte[32][])
//	args[2] = public inputs (byte[32][]): [amount, commitment]
//	args[3] = depositor address (32 raw bytes)
func (s *Subscriber) handleDeposit(
	args [][]byte, leafIndex uint64, treeRoot []byte, txnID string, block uint64,
) error {
	if len(args) < 4 {
		return fmt.Errorf("deposit: not enough args (%d)", len(args))
	}
	pubInputs := args[2]
	amountBytes := getByte32(pubInputs, 0)
	amount := binary.BigEndian.Uint64(amountBytes[24:])
	commitment := getByte32(pubInputs, 1)

	// args[3] contains 32 raw bytes representing the Algorand public key
	address := ""
	if len(args[3]) == 32 {
		var addr types.Address
		copy(addr[:], args[3])
		address = addr.String()
	}

	return db.SaveDeposit(leafIndex, commitment, txnID, address, amount, treeRoot, block)
}

// handleWithdrawal extracts withdrawal data from transaction arguments and saves to db.
//
// ARC-4 withdrawal args:
//
//	args[0] = method selector
//	args[1] = ZK proof (byte[32][])
//	args[2] = public inputs (byte[32][]): [recipient_mod, amount, fee, commitment, nullifier, root]
//	args[3] = withdrawal account position (1-based)
//	accounts = foreign accounts array (from indexer; nil for algod-only path)
func (s *Subscriber) handleWithdrawal(
	args [][]byte, accounts []string,
	leafIndex uint64, treeRoot []byte, txnID string, block uint64,
) error {
	if len(args) < 5 {
		return fmt.Errorf("withdrawal: not enough args (%d)", len(args))
	}
	pubInputs := args[2]
	amountBytes := getByte32(pubInputs, 1)
	amount := binary.BigEndian.Uint64(amountBytes[24:])
	feeBytes := getByte32(pubInputs, 2)
	fee := binary.BigEndian.Uint64(feeBytes[24:])
	commitment := getByte32(pubInputs, 3)
	nullifier := getByte32(pubInputs, 4)

	// args[3] encodes 1-based position into the accounts array
	address := ""
	if len(accounts) > 0 && len(args[3]) > 0 {
		pos := int(args[3][len(args[3])-1]) - 1
		if pos >= 0 && pos < len(accounts) {
			address = accounts[pos]
		}
	}

	return db.SaveWithdrawal(leafIndex, commitment, txnID, address, amount, fee, nullifier, treeRoot, block)
}

// ────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────

// parseTxnLog decodes the ARC-4 return value (uint64, byte[32]) from a log entry.
// Format: 4-byte prefix + 8-byte leaf index + 32-byte tree root = 44 bytes total.
func parseTxnLog(logEntry []byte) (leafIndex uint64, treeRoot []byte, err error) {
	if len(logEntry) != 44 {
		return 0, nil, fmt.Errorf("log has invalid length %d; expected 44", len(logEntry))
	}
	leafIndex = binary.BigEndian.Uint64(logEntry[4:12])
	treeRoot = make([]byte, 32)
	copy(treeRoot, logEntry[12:44])
	return leafIndex, treeRoot, nil
}

// getByte32 extracts the 32-byte element at position pos from an ARC-4 byte[32][] array.
// The first 2 bytes encode the array length; each element occupies 32 bytes.
func getByte32(array []byte, pos int) []byte {
	start := 2 + pos*32
	if start+32 > len(array) {
		return make([]byte, 32)
	}
	return array[start : start+32]
}

func equalSelector(a, b []byte) bool {
	if len(a) < 4 || len(b) < 4 {
		return false
	}
	return a[0] == b[0] && a[1] == b[1] && a[2] == b[2] && a[3] == b[3]
}
