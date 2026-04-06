package cmd

import (
	"crypto/ed25519"
	"fmt"
	"os"
	"strings"

	"github.com/nullun/hermes-vault-cli/internal/avm"
	"github.com/nullun/hermes-vault-cli/internal/config"
	"github.com/nullun/hermes-vault-cli/internal/db"
	"github.com/nullun/hermes-vault-cli/internal/logger"
	"github.com/nullun/hermes-vault-cli/internal/models"

	"github.com/algorand/go-algorand-sdk/v2/crypto"
	"github.com/algorand/go-algorand-sdk/v2/mnemonic"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var depositCmd = &cobra.Command{
	Use:   "deposit <amount>",
	Short: "Deposit ALGO into the shielded pool",
	Long: `Deposit ALGO into the Hermes Vault shielded pool.

You will receive a secret note. Keep it safe — it is the only way to
withdraw your funds. The note encodes the amount, and two random nonces.

Example:
  hermes deposit 10
  hermes deposit 10.5 --address ADDR...
  hermes deposit --all --address ADDR...`,
	Args: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		if all && len(args) > 0 {
			return helpOnUsageError(cmd)
		}
		if !all && len(args) != 1 {
			return helpOnUsageError(cmd)
		}
		return nil
	},
	RunE: runDeposit,
}

var depositAddress string
var depositAll bool
var depositSimulate bool

var logDurableFn = logger.LogDurable
var registerUnconfirmedNoteFn = db.RegisterUnconfirmedNote
var saveConfirmedNoteFn = db.SaveNote

func recordPendingNote(logFormat string, note *models.Note) (int64, error) {
	if err := logDurableFn(logFormat, note.TxnID, note.Amount, note.Text()); err != nil {
		return 0, fmt.Errorf("could not record your note in the log file, so the transaction was not submitted: %w", err)
	}

	noteID, err := registerUnconfirmedNoteFn(note)
	if err != nil {
		return 0, fmt.Errorf("could not record your note in the local database, so the transaction was not submitted: %w", err)
	}
	return noteID, nil
}

func persistConfirmedNote(note *models.Note) error {
	if err := saveConfirmedNoteFn(note); err != nil {
		return fmt.Errorf("the transaction confirmed, but the note could not be saved to the local database. The recovery record is still available, but Hermes will not treat this as a successful completion: %w", err)
	}
	return nil
}

func init() {
	depositCmd.Flags().StringVar(&depositAddress, "address", "",
		"Algorand address to deposit from (overrides config UserAddress)")
	depositCmd.Flags().BoolVar(&depositAll, "all", false,
		"Deposit the entire account balance (closes the account into the pool)")
	depositCmd.Flags().BoolVar(&depositSimulate, "simulate", false,
		"Sign transactions and run against the algod simulation endpoint without submitting; saves a .stxn file for inspection")
}

func runDeposit(cmd *cobra.Command, args []string) error {
	if err := ensureRuntime(); err != nil {
		return err
	}

	// Resolve address first — needed for --all balance lookup
	addrStr := depositAddress
	if addrStr == "" {
		addrStr = config.UserAddress
	}
	if addrStr == "" {
		return fmt.Errorf("no address specified; use --address or set UserAddress in config")
	}
	address, err := models.Input(addrStr).ToAddress()
	if err != nil {
		return fmt.Errorf("invalid address: %w", err)
	}

	var amount models.Amount
	if depositAll {
		amount, err = rt.avm.MaxDepositAmount(string(address))
		if err != nil {
			return fmt.Errorf("could not determine maximum deposit amount: %w", err)
		}
		fmt.Printf("Depositing entire balance (%s ALGO) from %s (account will be closed)...\n\n",
			amount.AlgoString, addrStr)
	} else {
		amount, err = models.Input(args[0]).ToAmount()
		if err != nil {
			return fmt.Errorf("invalid amount %q: %w", args[0], err)
		}
		if amount.MicroAlgos < config.DepositMinimumAmount {
			return fmt.Errorf("minimum deposit is %s ALGO",
				models.MicroAlgosToAlgoString(config.DepositMinimumAmount))
		}
		fmt.Printf("Depositing %s ALGO from %s...\n\n", amount.AlgoString, addrStr)
	}

	// Get private key — use stored mnemonic if available, else prompt
	privateKey, derivedAddress, err := getMnemonicKey()
	if err != nil {
		return err
	}

	// Verify address matches mnemonic
	if !strings.EqualFold(derivedAddress, string(address)) {
		return fmt.Errorf("mnemonic does not match address %s (derived: %s)",
			address, derivedAddress)
	}

	// Generate secret note
	note, err := models.GenerateNote(amount.MicroAlgos)
	if err != nil {
		return fmt.Errorf("failed to generate note: %w", err)
	}

	// Create transaction group
	fmt.Println("Generating zero-knowledge proof (this may take a moment)...")
	txns, err := rt.avm.CreateDepositTxns(amount, address, note)
	if err != nil {
		return fmt.Errorf("failed to create deposit transactions: %w", err)
	}

	// Set the note's TxnID
	note.TxnID = crypto.GetTxID(txns[0])

	// ── Simulate path ────────────────────────────────────────────────────────
	if depositSimulate {
		fmt.Println("Signing transaction group (not submitting)...")
		signedGroup, err := rt.avm.SignDepositGroup(txns, privateKey)
		if err != nil {
			return fmt.Errorf("signing transactions: %w", err)
		}
		path, err := saveStxnFile(signedGroup, note.TxnID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save .stxn file: %v\n", err)
		} else {
			fmt.Printf("Signed transactions saved to: %s\n", path)
		}
		fmt.Println("Calling simulation endpoint...")
		resp, err := rt.avm.SimulateGroup(signedGroup, len(txns))
		if err != nil {
			return fmt.Errorf("simulation request failed: %w", err)
		}
		printSimulateResult(resp)
		fmt.Println()
		fmt.Println("══════════════════════════════════════════════════")
		fmt.Println("  NOTE (SIMULATED — NOT SUBMITTED, NOT SAVED):")
		fmt.Println()
		fmt.Printf("  %s\n", note.Text())
		fmt.Printf("  Amount: %s ALGO\n", amount.AlgoString)
		fmt.Println("══════════════════════════════════════════════════")
		fmt.Println()
		fmt.Println("This note is not valid — it was generated for simulation only.")
		return nil
	}

	// ── Live path ─────────────────────────────────────────────────────────────
	// Log note to file before any network interaction — this is the safety net.
	// Even if the process crashes after confirmation, the note text is recoverable from the log.
	noteID, err := recordPendingNote("DEPOSIT NOTE_GENERATED txn=%s amount_ualgos=%d note=%s", note)
	if err != nil {
		return err
	}

	fmt.Println("Submitting transaction group to network...")
	leafIndex, txnID, confirmErr := rt.avm.SendDepositToNetwork(txns, privateKey)
	if confirmErr != nil {
		// Clean up the unconfirmed record unless we timed out (txn may still confirm)
		if confirmErr.Type != avm.TxnTimeout {
			_ = db.DeleteUnconfirmedNote(noteID)
		} else {
			logger.Log("DEPOSIT TIMEOUT txn=%s — unconfirmed note retained in db", note.TxnID)
		}
		return handleDepositError(confirmErr, address)
	}

	logger.Log("DEPOSIT CONFIRMED txn=%s leaf_index=%d note=%s", txnID, leafIndex, note.Text())

	note.LeafIndex = leafIndex
	if txnID != note.TxnID {
		fmt.Fprintf(os.Stderr, "Warning: txnId mismatch %s != %s\n", txnID, note.TxnID)
	}

	// Save confirmed note, then remove the unconfirmed record.
	// Order matters: if SaveNote panics or fails, the unconfirmed record remains
	// as a recovery mechanism for the cleanup routine.
	if err := persistConfirmedNote(note); err != nil {
		logger.Log("DEPOSIT ERROR failed to save note txn=%s: %v — note=%s", txnID, err, note.Text())
		return err
	} else {
		_ = db.DeleteUnconfirmedNote(noteID)
		logger.Log("DEPOSIT NOTE_SAVED txn=%s leaf_index=%d", txnID, leafIndex)
	}

	fmt.Printf("\n✓ Deposit successful! Leaf index: %d\n\n", leafIndex)
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Println("  YOUR SECRET NOTE (save this securely):")
	fmt.Println()
	fmt.Printf("  %s\n", note.Text())
	fmt.Println()
	fmt.Printf("  Amount: %s ALGO\n", amount.AlgoString)
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Println()
	fmt.Println("⚠️  This note is the ONLY way to withdraw your funds.")
	fmt.Println("   Store it in a password manager or other secure location.")

	return nil
}

func handleDepositError(e *avm.TxnConfirmationError, address models.Address) error {
	switch e.Type {
	case avm.TxnOverSpend, avm.TxnMinimumBalanceRequirement:
		bal, mbr, _ := rt.avm.GetBalanceAndMBR(string(address))
		available := uint64(0)
		fee := uint64(config.DepositMinFeeMultiplier * 1000)
		if bal > mbr+fee {
			available = bal - mbr - fee
		}
		return fmt.Errorf("insufficient funds. Available to deposit: %s ALGO",
			models.MicroAlgosToAlgoString(available))
	case avm.TxnExpired:
		return fmt.Errorf("transaction expired; please try again")
	case avm.TxnTimeout:
		return fmt.Errorf("transaction submitted but confirmation timed out.\n" +
			"Check your wallet — the deposit may still confirm.\n" +
			"If it does, run 'hermes sync' and then 'hermes notes' to see it.")
	default:
		return fmt.Errorf("deposit failed: %w", e)
	}
}

// getMnemonicKey returns the private key and derived address from the stored
// config mnemonic, or prompts for one if not configured.
func getMnemonicKey() (ed25519.PrivateKey, string, error) {
	mn := strings.TrimSpace(config.Mnemonic)
	if mn == "" {
		fmt.Println("This requires signing a payment transaction.")
		fmt.Print("Enter your Algorand mnemonic (25 words, input hidden): ")
		mnemonicBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return nil, "", fmt.Errorf("failed to read mnemonic: %w", err)
		}
		mn = strings.TrimSpace(string(mnemonicBytes))
	}

	privateKey, err := mnemonic.ToPrivateKey(mn)
	if err != nil {
		return nil, "", fmt.Errorf("invalid mnemonic: %w", err)
	}

	account, err := crypto.AccountFromPrivateKey(privateKey)
	if err != nil {
		return nil, "", fmt.Errorf("failed to derive account: %w", err)
	}

	return privateKey, account.Address.String(), nil
}
