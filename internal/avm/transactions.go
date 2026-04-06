package avm

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"fmt"

	"github.com/nullun/hermes-vault-cli/internal/config"
	"github.com/nullun/hermes-vault-cli/internal/logger"
	"github.com/nullun/hermes-vault-cli/internal/models"
	"github.com/nullun/hermes-vault-cli/internal/zkp"
	"github.com/nullun/hermes-vault-cli/internal/zkp/circuits"

	sdk_models "github.com/algorand/go-algorand-sdk/v2/client/v2/common/models"
	"github.com/algorand/go-algorand-sdk/v2/crypto"
	sdkMsgpack "github.com/algorand/go-algorand-sdk/v2/encoding/msgpack"
	"github.com/algorand/go-algorand-sdk/v2/transaction"
	"github.com/algorand/go-algorand-sdk/v2/types"
	"github.com/consensys/gnark/frontend"
)

// CreateDepositTxns constructs the transaction group for a deposit:
//  1. App call signed by the DepositVerifier logic sig (ZK proof)
//  2. Payment to the contract address (to be signed by user's key)
//  3. NoOp calls signed by TSS (opcode budget)
func (c *Client) CreateDepositTxns(amount models.Amount, userAddress models.Address, note *models.Note,
) ([]types.Transaction, error) {
	assignment := &circuits.DepositCircuit{
		Amount:     amount.MicroAlgos,
		Commitment: note.Commitment(),
		K:          note.K[:],
		R:          note.R[:],
	}
	zkArgs, err := zkp.ZKArgs(assignment, c.App.DepositCc)
	if err != nil {
		return nil, fmt.Errorf("failed to get zk args for deposit: %w", err)
	}

	depositMethod, err := c.App.Schema.Contract.GetMethodByName(config.DepositMethodName)
	if err != nil {
		return nil, fmt.Errorf("failed to get method %s: %w", config.DepositMethodName, err)
	}
	appArgs := [][]byte{depositMethod.GetSelector()}
	appArgs = append(appArgs, zkArgs...)

	addressBytes, err := types.DecodeAddress(string(userAddress))
	if err != nil {
		return nil, fmt.Errorf("failed to decode address: %w", err)
	}
	appArgs = append(appArgs, addressBytes[:])

	sp, err := c.Algod.SuggestedParams().Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get suggested params: %w", err)
	}
	sp.Fee = 0
	sp.FlatFee = true
	sp.LastRoundValid = sp.FirstRoundValid + config.WaitRounds

	// txn1: app call signed by DepositVerifier
	txn1, err := transaction.MakeApplicationNoOpTxWithBoxes(
		c.App.ID, appArgs,
		nil, nil, nil,
		[]types.AppBoxReference{
			{AppID: c.App.ID, Name: []byte("subtree")},
			{AppID: c.App.ID, Name: []byte("subtree")},
			{AppID: c.App.ID, Name: []byte("roots")},
			{AppID: c.App.ID, Name: []byte("roots")},
		},
		sp, c.App.DepositVerifier.Address, nil, types.Digest{}, [32]byte{}, types.ZeroAddress,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to make app call txn: %w", err)
	}

	// txn2: payment signed by user
	txnFee := types.MicroAlgos(transaction.MinTxnFee * config.DepositMinFeeMultiplier)
	contractAddress := crypto.GetApplicationAddress(c.App.ID).String()
	closeRemainderTo := types.ZeroAddress.String()

	accountInfo, err := c.Algod.AccountInformation(string(userAddress)).Do(context.Background())
	if err != nil {
		logger.Log("could not get account info for close-out optimisation: %v", err)
	} else if accountInfo.Amount >= uint64(txnFee) &&
		amount.MicroAlgos == accountInfo.Amount-uint64(txnFee) {
		closeRemainderTo = contractAddress
	}

	txn2, err := transaction.MakePaymentTxn(
		string(userAddress), contractAddress, amount.MicroAlgos,
		nil, closeRemainderTo, sp,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to make payment txn: %w", err)
	}
	txn2.Fee = txnFee

	// Additional NoOp transactions for opcode budget
	noopMethod, err := c.App.Schema.Contract.GetMethodByName(config.NoOpMethodName)
	if err != nil {
		return nil, fmt.Errorf("failed to get method %s: %w", config.NoOpMethodName, err)
	}
	noopArgs := [][]byte{noopMethod.GetSelector()}
	txnNeeded := config.VerifierTopLevelTxnNeeded - 2

	txns := []types.Transaction{txn1, txn2}
	for i := range txnNeeded {
		txn, err := transaction.MakeApplicationNoOpTx(
			c.App.ID, append(noopArgs, []byte{byte(i)}),
			nil, nil, nil,
			sp, c.App.TSS.Address, nil, types.Digest{}, [32]byte{}, types.ZeroAddress,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to make noop txn: %w", err)
		}
		txns = append(txns, txn)
	}

	groupID, err := crypto.ComputeGroupID(txns)
	if err != nil {
		return nil, fmt.Errorf("failed to compute group id: %w", err)
	}
	for i := range txns {
		txns[i].Group = groupID
	}
	return txns, nil
}

// SignDepositGroup signs the deposit transaction group and returns the concatenated
// msgpack-encoded signed-transaction bytes, ready for submission or simulation.
func (c *Client) SignDepositGroup(txns []types.Transaction, userPrivateKey ed25519.PrivateKey) ([]byte, error) {
	_, signed1, err := crypto.SignLogicSigAccountTransaction(c.App.DepositVerifier.Account, txns[0])
	if err != nil {
		return nil, fmt.Errorf("signing app call txn: %w", err)
	}
	_, signedPayment, err := crypto.SignTransaction(userPrivateKey, txns[config.UserDepositTxnIndex])
	if err != nil {
		return nil, fmt.Errorf("signing payment txn: %w", err)
	}
	group := append(signed1, signedPayment...)
	for i := 2; i < len(txns); i++ {
		_, signed, err := crypto.SignLogicSigAccountTransaction(c.App.TSS.Account, txns[i])
		if err != nil {
			return nil, fmt.Errorf("signing noop txn %d: %w", i, err)
		}
		group = append(group, signed...)
	}
	return group, nil
}

// SendDepositToNetwork signs the deposit transaction group and submits it.
// Returns the leaf index, transaction ID, and any error.
func (c *Client) SendDepositToNetwork(txns []types.Transaction, userPrivateKey ed25519.PrivateKey,
) (leafIndex uint64, txnId string, txnConfirmationError *TxnConfirmationError) {
	signedGroup, err := c.SignDepositGroup(txns, userPrivateKey)
	if err != nil {
		return 0, "", NewInternalError(err.Error())
	}
	return c.submitAndWait(signedGroup, crypto.GetTxID(txns[0]))
}

// CreateWithdrawalTxns constructs the transaction group for a withdrawal.
func (c *Client) CreateWithdrawalTxns(w *models.WithdrawalData) ([]types.Transaction, error) {
	if w.FromNote.LeafIndex == models.EmptyLeafIndex {
		return nil, fmt.Errorf("empty leaf index")
	}

	root, err := getRoot(w.FromNote.LeafIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to get root: %w", err)
	}

	merkleProof, err := c.createMerkleProof(w.FromNote.LeafValue(), w.FromNote.LeafIndex, root)
	if err != nil {
		return nil, fmt.Errorf("failed to create merkle proof: %w", err)
	}
	var path [config.MerkleTreeLevels + 1]frontend.Variable
	for i, v := range merkleProof {
		path[i] = v
	}

	withdrawalRecipient, err := types.DecodeAddress(string(w.Address))
	if err != nil {
		return nil, fmt.Errorf("failed to decode recipient address: %w", err)
	}

	assignment := &circuits.WithdrawalCircuit{
		Recipient:  withdrawalRecipient[:],
		Withdrawal: w.Amount.MicroAlgos,
		Fee:        w.Fee.MicroAlgos,
		Commitment: w.ChangeNote.Commitment(),
		Nullifier:  w.FromNote.Nullifier(),
		Root:       root,
		K:          w.FromNote.K[:],
		R:          w.FromNote.R[:],
		Amount:     w.FromNote.Amount,
		Change:     w.ChangeNote.Amount,
		K2:         w.ChangeNote.K[:],
		R2:         w.ChangeNote.R[:],
		Index:      w.FromNote.LeafIndex,
		Path:       path,
	}
	zkArgs, err := zkp.ZKArgs(assignment, c.App.WithdrawalCc)
	if err != nil {
		return nil, fmt.Errorf("failed to get zk args for withdrawal: %w", err)
	}

	withdrawalMethod, err := c.App.Schema.Contract.GetMethodByName(config.WithdrawalMethodName)
	if err != nil {
		return nil, fmt.Errorf("failed to get method %s: %w", config.WithdrawalMethodName, err)
	}
	args := [][]byte{withdrawalMethod.GetSelector()}
	args = append(args, zkArgs...)

	foreignAccounts := []string{withdrawalRecipient.String()}
	args = append(args, []byte{1}) // withdrawal recipient at accounts[1]

	feeRecipient := c.App.TSS.Address
	foreignAccounts = append(foreignAccounts, feeRecipient.String())
	args = append(args, []byte{2}) // fee recipient at accounts[2]

	noChange := false
	noChangeAbi, err := abiEncode(noChange, "bool")
	if err != nil {
		return nil, fmt.Errorf("failed to encode noChange: %w", err)
	}
	args = append(args, noChangeAbi)

	sp, err := c.Algod.SuggestedParams().Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get suggested params: %w", err)
	}
	sp.Fee = 0
	sp.FlatFee = true
	sp.LastRoundValid = sp.FirstRoundValid + config.WaitRounds

	// txn1: app call signed by WithdrawalVerifier
	txn1, err := transaction.MakeApplicationNoOpTxWithBoxes(
		c.App.ID, args, foreignAccounts, nil, nil,
		[]types.AppBoxReference{
			{AppID: c.App.ID, Name: w.FromNote.Nullifier()},
			{AppID: c.App.ID, Name: []byte("subtree")},
			{AppID: c.App.ID, Name: []byte("roots")},
			{AppID: c.App.ID, Name: []byte("roots")},
		},
		sp, c.App.WithdrawalVerifier.Address, nil, types.Digest{}, [32]byte{}, types.ZeroAddress,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to make withdrawal app call txn: %w", err)
	}

	noopMethod, err := c.App.Schema.Contract.GetMethodByName(config.NoOpMethodName)
	if err != nil {
		return nil, fmt.Errorf("failed to get method %s: %w", config.NoOpMethodName, err)
	}
	noopArgs := [][]byte{noopMethod.GetSelector()}
	txns := []types.Transaction{txn1}
	txnNeeded := config.VerifierTopLevelTxnNeeded - 1

	for i := range txnNeeded {
		txn, err := transaction.MakeApplicationNoOpTx(
			c.App.ID, append(noopArgs, []byte{byte(i)}),
			nil, nil, nil,
			sp, feeRecipient, nil, types.Digest{}, [32]byte{}, types.ZeroAddress,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to make noop txn: %w", err)
		}
		txns = append(txns, txn)
	}
	if len(txns) < 2 {
		return nil, fmt.Errorf("withdrawal requires at least 2 transactions, got %d", len(txns))
	}
	txns[1].Fee = transaction.MinTxnFee * config.WithdrawalMinFeeMultiplier

	groupID, err := crypto.ComputeGroupID(txns)
	if err != nil {
		return nil, fmt.Errorf("failed to compute group id: %w", err)
	}
	for i := range txns {
		txns[i].Group = groupID
	}
	return txns, nil
}

// SignWithdrawalGroup signs the withdrawal transaction group and returns the concatenated
// msgpack-encoded signed-transaction bytes, ready for submission or simulation.
func (c *Client) SignWithdrawalGroup(txns []types.Transaction) ([]byte, error) {
	_, signed1, err := crypto.SignLogicSigAccountTransaction(c.App.WithdrawalVerifier.Account, txns[0])
	if err != nil {
		return nil, fmt.Errorf("signing withdrawal app call txn: %w", err)
	}
	group := signed1
	for i := 1; i < len(txns); i++ {
		_, signed, err := crypto.SignLogicSigAccountTransaction(c.App.TSS.Account, txns[i])
		if err != nil {
			return nil, fmt.Errorf("signing noop txn %d: %w", i, err)
		}
		group = append(group, signed...)
	}
	return group, nil
}

// SendWithdrawalToNetwork signs and submits the withdrawal transaction group using logic sigs.
// No user private key is required for withdrawals.
func (c *Client) SendWithdrawalToNetwork(txns []types.Transaction,
) (leafIndex uint64, txnId string, txnConfirmationError *TxnConfirmationError) {
	signedGroup, err := c.SignWithdrawalGroup(txns)
	if err != nil {
		return 0, "", NewInternalError(err.Error())
	}
	return c.submitAndWait(signedGroup, crypto.GetTxID(txns[0]))
}

// submitAndWait submits a signed transaction group and waits for confirmation.
// appCallTxnID should be the ID of the first (app call) transaction in the group.
func (c *Client) submitAndWait(signedGroup []byte, appCallTxnID string) (uint64, string, *TxnConfirmationError) {
	_, err := c.Algod.SendRawTransaction(signedGroup).Do(context.Background())
	if err != nil {
		return 0, "", parseSendTransactionError(err)
	}
	confirmedTxn, err := transaction.WaitForConfirmation(c.Algod, appCallTxnID,
		config.WaitRounds, context.Background())
	if err != nil {
		return 0, "", parseWaitForConfirmationError(err)
	}
	leafIndex, _, err := getLeafIndexAndRoot(confirmedTxn)
	if err != nil {
		return 0, "", NewInternalError("failed to get leaf index: " + err.Error())
	}
	return leafIndex, appCallTxnID, nil
}

// SimulateGroup decodes the signed transaction group and calls the algod simulation endpoint.
func (c *Client) SimulateGroup(signedGroup []byte, txnCount int) (sdk_models.SimulateResponse, error) {
	dec := sdkMsgpack.NewLenientDecoder(bytes.NewReader(signedGroup))
	signed := make([]types.SignedTxn, txnCount)
	for i := range signed {
		if err := dec.Decode(&signed[i]); err != nil {
			return sdk_models.SimulateResponse{}, fmt.Errorf("decoding signed txn %d: %w", i, err)
		}
	}
	request := sdk_models.SimulateRequest{
		AllowMoreLogging: true,
		TxnGroups:        []sdk_models.SimulateRequestTransactionGroup{{Txns: signed}},
	}
	return c.Algod.SimulateTransaction(request).Do(context.Background())
}

// getLeafIndexAndRoot parses the leaf index and Merkle root from a confirmed transaction's logs.
func getLeafIndexAndRoot(txn sdk_models.PendingTransactionInfoResponse,
) (leafIndex uint64, root [32]byte, err error) {
	if len(txn.Logs) == 0 {
		return 0, root, fmt.Errorf("no logs in transaction")
	}
	abiBytes := txn.Logs[len(txn.Logs)-1]
	if len(abiBytes) != 4+8+32 {
		return 0, root, fmt.Errorf("invalid log length: expected 44 bytes, got %d", len(abiBytes))
	}
	leafIndex = binary.BigEndian.Uint64(abiBytes[4:12])
	copy(root[:], abiBytes[12:])
	return leafIndex, root, nil
}
