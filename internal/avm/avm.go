// Package avm provides functionality to interact with Algorand smart contracts.
package avm

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/nullun/hermes-vault-cli/internal/config"
	"github.com/nullun/hermes-vault-cli/internal/models"
	"github.com/nullun/hermes-vault-cli/internal/resources"

	"github.com/algorand/go-algorand-sdk/v2/abi"
	"github.com/algorand/go-algorand-sdk/v2/client/v2/algod"
	"github.com/algorand/go-algorand-sdk/v2/transaction"
)

// Client holds the initialised algod client, smart contract artifacts, and
// runtime constants. Create one with New and pass it to callers that need
// blockchain access.
type Client struct {
	Algod            *algod.Client
	App              *models.App
	AppCreationBlock uint64
	MinimumBalance   uint64
}

// New creates a fully initialised Client. It connects to algod, loads smart
// contract artifacts from embedded resources, and fetches the minimum balance.
func New(algodURL, algodToken, network string) (*Client, error) {
	if algodURL == "" {
		algodURL = "http://localhost:4001"
		if algodToken == "" {
			algodToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}
	}

	algodClient, err := algod.MakeClient(algodURL, algodToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create algod client: %w", err)
	}

	app, creationBlock, err := setupApp(resources.Resources, network)
	if err != nil {
		return nil, err
	}

	c := &Client{
		Algod:            algodClient,
		App:              app,
		AppCreationBlock: creationBlock,
	}
	c.MinimumBalance = c.getMinimumBalance()
	return c, nil
}

// GetBalanceAndMBR returns the balance and minimum balance requirement of address.
func (c *Client) GetBalanceAndMBR(address string) (uint64, uint64, error) {
	info, err := c.Algod.AccountInformation(address).Do(context.Background())
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get account information: %w", err)
	}
	return info.Amount, info.MinBalance, nil
}

// MaxDepositAmount returns the maximum amount depositable from address,
// which is the full account balance minus the deposit transaction fee.
// Using this amount will trigger a close-out of the account into the pool.
func (c *Client) MaxDepositAmount(address string) (models.Amount, error) {
	balance, _, err := c.GetBalanceAndMBR(address)
	if err != nil {
		return models.Amount{}, err
	}
	fee := uint64(transaction.MinTxnFee * config.DepositMinFeeMultiplier)
	if balance <= fee {
		return models.Amount{}, fmt.Errorf("account balance (%s ALGO) does not cover the deposit fee (%s ALGO)",
			models.MicroAlgosToAlgoString(balance), models.MicroAlgosToAlgoString(fee))
	}
	return models.NewAmount(balance - fee), nil
}

// abiEncode encodes arg into its ABI byte representation.
func abiEncode(arg any, abiTypeName string) ([]byte, error) {
	abiType, err := abi.TypeOf(abiTypeName)
	if err != nil {
		return nil, fmt.Errorf("failed to get abi type: %w", err)
	}
	abiArg, err := abiType.Encode(arg)
	if err != nil {
		return nil, fmt.Errorf("failed to encode arg: %w", err)
	}
	return abiArg, nil
}

// CompileTeal compiles TEAL source and returns the bytecode.
func (c *Client) CompileTeal(tealPath string) ([]byte, error) {
	teal, err := os.ReadFile(tealPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", tealPath, err)
	}
	result, err := c.Algod.TealCompile(teal).Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to compile %s: %w", tealPath, err)
	}
	binary, err := base64.StdEncoding.DecodeString(result.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to decode compiled TEAL: %w", err)
	}
	return binary, nil
}
