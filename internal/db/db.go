// Package db provides unified database operations for Hermes.
// All data (notes, chain transactions, sync state) is stored in a single SQLite file.
package db

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nullun/hermes-vault-cli/internal/logger"
	"github.com/nullun/hermes-vault-cli/internal/models"

	_ "modernc.org/sqlite"
)

var db *sql.DB

// Open initialises the unified SQLite database at dbPath.
// It must be called once before any other db function.
func Open(dbPath string) error {
	var err error
	db, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)
	if err := migrate(); err != nil {
		return err
	}

	// Best-effort: tighten permissions on the db and its WAL/SHM files.
	// Some may not exist yet (SQLite creates them lazily), which is fine.
	setRestrictivePermissions(dbPath)
	setRestrictivePermissions(dbPath + "-wal")
	setRestrictivePermissions(dbPath + "-shm")
	return nil
}

// Close closes the database connection.
func Close() {
	if db != nil {
		if err := db.Close(); err != nil {
			logger.Log("Error closing database: %v", err)
		}
	}
}

func setRestrictivePermissions(path string) {
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.Log("Warning: could not stat %s: %v", path, err)
		}
		return
	}
	if err := os.Chmod(path, 0600); err != nil {
		logger.Log("Warning: could not set permissions on %s: %v", path, err)
	}
}

func migrate() error {
	schema := `
	-- User's confirmed private notes
	CREATE TABLE IF NOT EXISTS notes (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		leaf_index INTEGER UNIQUE NOT NULL,
		commitment BLOB    NOT NULL,
		txn_id     TEXT    NOT NULL,
		nullifier  BLOB    NOT NULL,
		note_text  TEXT    NOT NULL
	) STRICT;

	-- Notes awaiting on-chain confirmation
	CREATE TABLE IF NOT EXISTS unconfirmed_notes (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		commitment BLOB    NOT NULL,
		nullifier  BLOB,
		txn_id     TEXT    UNIQUE NOT NULL,
		note_text  TEXT,
		created_at TEXT    DEFAULT CURRENT_TIMESTAMP
	) STRICT;

	-- All on-chain deposit/withdrawal transactions observed by subscriber
	CREATE TABLE IF NOT EXISTS txns (
		leaf_index     INTEGER PRIMARY KEY,
		commitment     BLOB    NOT NULL,
		txn_id         TEXT    UNIQUE NOT NULL,
		txn_type       INTEGER NOT NULL,
		address        TEXT    NOT NULL,
		amount         INTEGER NOT NULL,
		from_nullifier BLOB
	) STRICT;

	CREATE INDEX IF NOT EXISTS idx_txns_commitment ON txns(commitment);
	CREATE INDEX IF NOT EXISTS idx_txns_nullifier ON txns(from_nullifier);

	-- Aggregate statistics
	CREATE TABLE IF NOT EXISTS stats (
		key   TEXT PRIMARY KEY,
		value INTEGER NOT NULL
	) STRICT;

	-- Sync watermark: highest confirmed round processed by subscriber
	CREATE TABLE IF NOT EXISTS watermark (
		id    INTEGER PRIMARY KEY CHECK (id = 1),
		value INTEGER NOT NULL
	) STRICT;

	-- Latest Merkle tree root observed on-chain
	CREATE TABLE IF NOT EXISTS roots (
		id         INTEGER PRIMARY KEY CHECK (id = 1),
		value      BLOB    NOT NULL,
		leaf_count INTEGER NOT NULL
	) STRICT;
	`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	// Seed stats keys
	statsKeys := [][2]interface{}{
		{"total_deposits", 0},
		{"total_withdrawals", 0},
		{"total_fees", 0},
		{"count_deposits", 0},
	}
	for _, kv := range statsKeys {
		if _, err := db.Exec(
			`INSERT OR IGNORE INTO stats (key, value) VALUES (?, ?)`, kv[0], kv[1]); err != nil {
			return fmt.Errorf("failed to seed stats: %w", err)
		}
	}
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO watermark (id, value) VALUES (1, 0)`); err != nil {
		return fmt.Errorf("failed to seed watermark: %w", err)
	}
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO roots (id, value, leaf_count) VALUES (1, x'', 0)`); err != nil {
		return fmt.Errorf("failed to seed roots: %w", err)
	}
	return nil
}

// ────────────────────────────────────────────────────────────────
// Note operations
// ────────────────────────────────────────────────────────────────

// RegisterUnconfirmedNote saves a note that has been submitted but not yet confirmed on-chain.
func RegisterUnconfirmedNote(n *models.Note) (int64, error) {
	res, err := db.Exec(
		`INSERT INTO unconfirmed_notes (commitment, nullifier, txn_id, note_text)
		 VALUES (?, ?, ?, ?)`,
		n.Commitment(), n.Nullifier(), n.TxnID, n.Text(),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to register unconfirmed note: %w", err)
	}
	return res.LastInsertId()
}

// SaveNote moves a note into the confirmed notes table.
func SaveNote(n *models.Note) error {
	if n.TxnID == models.EmptyTxnID || n.LeafIndex == models.EmptyLeafIndex {
		return fmt.Errorf("malformed confirmed note")
	}
	_, err := db.Exec(
		`INSERT OR REPLACE INTO notes (leaf_index, commitment, txn_id, nullifier, note_text)
		 VALUES (?, ?, ?, ?, ?)`,
		n.LeafIndex, n.Commitment(), n.TxnID, n.Nullifier(), n.Text(),
	)
	return err
}

// DeleteUnconfirmedNote removes a pending note by its row ID.
func DeleteUnconfirmedNote(id int64) error {
	_, err := db.Exec(`DELETE FROM unconfirmed_notes WHERE id = ?`, id)
	return err
}

// ListNotes returns confirmed notes stored for this wallet.
func ListNotes(includeUsed bool) ([]models.Note, error) {
	// A note is considered used if its nullifier exists in the txns table.
	query := `
		SELECT leaf_index, commitment, txn_id, note_text,
		       (EXISTS (SELECT 1 FROM txns WHERE from_nullifier = notes.nullifier)) as used
		FROM notes`

	if !includeUsed {
		query += ` WHERE used = 0`
	}
	query += ` ORDER BY leaf_index ASC`

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []models.Note
	for rows.Next() {
		var leafIndex uint64
		var commitment []byte
		var txnID, noteText string
		var isUsed bool
		if err := rows.Scan(&leafIndex, &commitment, &txnID, &noteText, &isUsed); err != nil {
			return nil, err
		}
		n, err := models.Input(noteText).ToNote()
		if err != nil {
			logger.Log("Warning: could not parse note text for leaf %d: %v", leafIndex, err)
			continue
		}

		// Filter out zero-amount notes unless explicitly requested
		if !includeUsed && n.Amount == 0 {
			continue
		}

		n.LeafIndex = leafIndex
		n.TxnID = txnID
		n.IsUsed = isUsed
		notes = append(notes, *n)
	}
	return notes, rows.Err()
}

// GetNoteByCommitment returns a confirmed note matching the given commitment.
func GetNoteByCommitment(commitment []byte) (*models.Note, error) {
	var leafIndex uint64
	var txnID, noteText string
	var isUsed bool
	err := db.QueryRow(
		`SELECT leaf_index, txn_id, note_text,
		        (EXISTS (SELECT 1 FROM txns WHERE from_nullifier = notes.nullifier))
		 FROM notes WHERE commitment = ?`,
		commitment,
	).Scan(&leafIndex, &txnID, &noteText, &isUsed)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	n, err := models.Input(noteText).ToNote()
	if err != nil {
		return nil, fmt.Errorf("parse saved note: %w", err)
	}
	n.LeafIndex = leafIndex
	n.TxnID = txnID
	n.IsUsed = isUsed
	return n, nil
}

// HasUnconfirmedNoteByCommitment reports whether a matching pending note exists.
func HasUnconfirmedNoteByCommitment(commitment []byte) (bool, error) {
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM unconfirmed_notes WHERE commitment = ?`,
		commitment,
	).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// DeleteUnconfirmedNoteByCommitment removes all pending notes for the commitment.
func DeleteUnconfirmedNoteByCommitment(commitment []byte) error {
	_, err := db.Exec(`DELETE FROM unconfirmed_notes WHERE commitment = ?`, commitment)
	return err
}

// ────────────────────────────────────────────────────────────────
// Chain transaction operations (used by subscriber)
// ────────────────────────────────────────────────────────────────

const (
	TxnTypeDeposit    = 0
	TxnTypeWithdrawal = 1
)

// SaveDeposit records a deposit observed on-chain.
func SaveDeposit(leafIndex uint64, commitment []byte, txnID string,
	address string, amount uint64, treeRoot []byte, block uint64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`INSERT OR IGNORE INTO txns (leaf_index, commitment, txn_id, txn_type, address, amount, from_nullifier)
		 VALUES (?, ?, ?, ?, ?, ?, NULL)`,
		leafIndex, commitment, txnID, TxnTypeDeposit, address, amount,
	)
	if err != nil {
		return fmt.Errorf("insert deposit txn: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("deposit insert rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return tx.Commit()
	}
	if _, err := tx.Exec(
		`UPDATE stats SET value = value + ? WHERE key = 'total_deposits'`, amount); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE stats SET value = value + 1 WHERE key = 'count_deposits'`); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE roots SET value = ?, leaf_count = ? WHERE id = 1`, treeRoot, leafIndex+1); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE watermark SET value = ? WHERE id = 1`, block); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveWithdrawal records a withdrawal observed on-chain.
func SaveWithdrawal(leafIndex uint64, commitment []byte, txnID string,
	address string, amount uint64, fee uint64, nullifier []byte,
	treeRoot []byte, block uint64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`INSERT OR IGNORE INTO txns (leaf_index, commitment, txn_id, txn_type, address, amount, from_nullifier)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		leafIndex, commitment, txnID, TxnTypeWithdrawal, address, amount, nullifier,
	)
	if err != nil {
		return fmt.Errorf("insert withdrawal txn: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("withdrawal insert rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return tx.Commit()
	}

	if _, err := tx.Exec(
		`UPDATE stats SET value = value + ? WHERE key = 'total_withdrawals'`, amount); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE stats SET value = value + ? WHERE key = 'total_fees'`, fee); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE roots SET value = ?, leaf_count = ? WHERE id = 1`, treeRoot, leafIndex+1); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE watermark SET value = ? WHERE id = 1`, block); err != nil {
		return err
	}
	return tx.Commit()
}

// GetLeafIndexByCommitment looks up the Merkle leaf index for a given note commitment.
func GetLeafIndexByCommitment(commitment []byte) (uint64, error) {
	var index uint64
	err := db.QueryRow(`SELECT leaf_index FROM txns WHERE commitment = ?`, commitment).Scan(&index)
	return index, err
}

// GetTxnByCommitment returns the chain transaction details for a commitment.
func GetTxnByCommitment(commitment []byte) (leafIndex uint64, txnID string, err error) {
	err = db.QueryRow(
		`SELECT leaf_index, txn_id FROM txns WHERE commitment = ?`,
		commitment,
	).Scan(&leafIndex, &txnID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	return leafIndex, txnID, err
}

// GetAllLeavesCommitments returns every committed leaf in leaf_index order.
func GetAllLeavesCommitments() ([][]byte, error) {
	rows, err := db.Query(`SELECT commitment FROM txns ORDER BY leaf_index ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commitments [][]byte
	for rows.Next() {
		var c []byte
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		commitments = append(commitments, c)
	}
	return commitments, rows.Err()
}

// GetRoot returns the latest known Merkle root and leaf count.
func GetRoot() (root []byte, leafCount uint64, err error) {
	err = db.QueryRow(`SELECT value, leaf_count FROM roots WHERE id = 1`).
		Scan(&root, &leafCount)
	return
}

// GetStats returns aggregate pool statistics.
func GetStats() (*models.StatData, error) {
	var depTotal, wdTotal, feeTotal, depCount uint64
	err := db.QueryRow(`
		SELECT
			(SELECT value FROM stats WHERE key = 'total_deposits'),
			(SELECT value FROM stats WHERE key = 'total_withdrawals'),
			(SELECT value FROM stats WHERE key = 'total_fees'),
			(SELECT value FROM stats WHERE key = 'count_deposits')`).
		Scan(&depTotal, &wdTotal, &feeTotal, &depCount)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}

	var noteCount uint64
	if err := db.QueryRow(`SELECT COUNT(*) FROM txns`).Scan(&noteCount); err != nil {
		return nil, err
	}

	return &models.StatData{
		DepositTotal:    models.NewAmount(depTotal),
		WithdrawalTotal: models.NewAmount(wdTotal),
		FeeTotal:        models.NewAmount(feeTotal),
		DepositCount:    depCount,
		NoteCount:       noteCount,
	}, nil
}

// ────────────────────────────────────────────────────────────────
// Watermark
// ────────────────────────────────────────────────────────────────

// GetWatermark returns the latest confirmed round processed by the subscriber.
func GetWatermark() (uint64, error) {
	var v uint64
	err := db.QueryRow(`SELECT value FROM watermark WHERE id = 1`).Scan(&v)
	return v, err
}

// SetWatermark stores the latest confirmed round processed by the subscriber.
func SetWatermark(round uint64) error {
	_, err := db.Exec(`UPDATE watermark SET value = ? WHERE id = 1`, round)
	return err
}

// ────────────────────────────────────────────────────────────────
// Cleanup
// ────────────────────────────────────────────────────────────────

// StartCleanupRoutine periodically resolves unconfirmed notes.
func StartCleanupRoutine(ctx context.Context, interval time.Duration) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				CleanupUnconfirmedNotes()
			case <-ctx.Done():
				return
			}
		}
	}()
	return cancel
}

// CleanupUnconfirmedNotes reconciles pending notes against confirmed on-chain data.
func CleanupUnconfirmedNotes() {
	rows, err := db.Query(
		`SELECT id, commitment, nullifier, txn_id, note_text, created_at FROM unconfirmed_notes`)
	if err != nil {
		logger.Log("cleanup: query unconfirmed_notes: %v", err)
		return
	}
	defer rows.Close()

	const timeLayout = "2006-01-02 15:04:05"

	type unconfirmedNote struct {
		id         int64
		commitment []byte
		nullifier  []byte
		txnID      string
		noteText   string
		createdAt  string
	}

	var notes []unconfirmedNote
	for rows.Next() {
		var id int64
		var commitment, nullifier []byte
		var txnID, noteText, createdAt string

		if err := rows.Scan(&id, &commitment, &nullifier, &txnID, &noteText, &createdAt); err != nil {
			logger.Log("cleanup: scan: %v", err)
			continue
		}
		notes = append(notes, unconfirmedNote{
			id:         id,
			commitment: commitment,
			nullifier:  nullifier,
			txnID:      txnID,
			noteText:   noteText,
			createdAt:  createdAt,
		})
	}

	if len(notes) == 0 {
		return
	}

	txnIDs := make([]string, 0, len(notes))
	for _, n := range notes {
		txnIDs = append(txnIDs, n.txnID)
	}

	txnLookup := make(map[string]struct {
		leafIndex      int64
		commitment     []byte
		hasTransaction bool
	})

	if len(txnIDs) > 0 {
		args := make([]interface{}, len(txnIDs))
		for i, id := range txnIDs {
			args[i] = id
		}
		txnRows, err := db.Query(
			`SELECT txn_id, leaf_index, commitment FROM txns WHERE txn_id IN (`+placeholderCommas(len(txnIDs))+`)`, args...)
		if err != nil {
			logger.Log("cleanup: query txns batch: %v", err)
		} else {
			defer txnRows.Close()
			for txnRows.Next() {
				var txnID string
				var leafIndex int64
				var commitment []byte
				if err := txnRows.Scan(&txnID, &leafIndex, &commitment); err != nil {
					logger.Log("cleanup: scan txn: %v", err)
					continue
				}
				txnLookup[txnID] = struct {
					leafIndex      int64
					commitment     []byte
					hasTransaction bool
				}{leafIndex: leafIndex, commitment: commitment, hasTransaction: true}
			}
		}
	}

	for _, n := range notes {
		noteTime, err := time.Parse(timeLayout, n.createdAt)
		if err != nil {
			continue
		}

		txn, exists := txnLookup[n.txnID]

		switch {
		case !exists:
			if time.Since(noteTime) > 7*24*time.Hour {
				logger.Log("cleanup: WARNING unconfirmed note id %d (txn %s) is >7 days old and not seen on-chain — check log file", n.id, n.txnID)
			}
		default:
			if bytes.Equal(txn.commitment, n.commitment) {
				func() {
					tx, err := db.Begin()
					if err != nil {
						return
					}
					defer func() { _ = tx.Rollback() }()

					if _, err := tx.Exec(
						`INSERT OR IGNORE INTO notes (leaf_index, commitment, txn_id, nullifier, note_text)
						 VALUES (?, ?, ?, ?, ?)`,
						txn.leafIndex, n.commitment, n.txnID, n.nullifier, n.noteText); err != nil {
						logger.Log("cleanup: insert note id %d: %v — keeping unconfirmed record", n.id, err)
						return
					}
					if _, err := tx.Exec(`DELETE FROM unconfirmed_notes WHERE id = ?`, n.id); err != nil {
						logger.Log("cleanup: delete unconfirmed note id %d: %v — keeping unconfirmed record", n.id, err)
						return
					}
					if err := tx.Commit(); err != nil {
						logger.Log("cleanup: commit note id %d: %v — keeping unconfirmed record", n.id, err)
					} else {
						logger.Log("cleanup: confirmed note id %d → leaf %d", n.id, txn.leafIndex)
					}
				}()
			}
		}
	}
}

func placeholderCommas(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}
