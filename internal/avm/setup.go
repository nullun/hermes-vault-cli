package avm

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/nullun/hermes-vault-cli/internal/config"
	"github.com/nullun/hermes-vault-cli/internal/logger"
	"github.com/nullun/hermes-vault-cli/internal/models"

	"github.com/algorand/go-algorand-sdk/v2/crypto"
	"github.com/giuliop/algoplonk/utils"
)

const (
	appFile                       = "App.json"
	appArc32File                  = "APP.arc32.json"
	tssTealFile                   = "TSS.tok"
	depositVerifierTealFile       = "DepositVerifier.tok"
	withdrawalVerifierTealFile    = "WithdrawalVerifier.tok"
	treeConfigFile                = "TreeConfig.json"
	compiledDepositCircuitFile    = "CompiledDepositCircuit.bin"
	compiledWithdrawalCircuitFile = "CompiledWithdrawalCircuit.bin"
)

type appJSON struct {
	ID            uint64 `json:"id"`
	CreationBlock uint64 `json:"creationBlock"`
}

// setupApp loads smart contract artifacts from embedded FS and returns the App and creation block.
func setupApp(resources fs.FS, network string) (*models.App, uint64, error) {
	pathTo := func(file string) string {
		return filepath.Join(network, file)
	}
	circuitPathTo := func(file string) string {
		return filepath.Join("circuits", file)
	}

	app := &models.App{}
	aj := appJSON{}

	if err := decodeJSONFile(resources, pathTo(appFile), &aj); err != nil {
		return nil, 0, fmt.Errorf("reading App.json: %w", err)
	}
	app.ID = aj.ID

	if err := decodeJSONFile(resources, pathTo(appArc32File), &app.Schema); err != nil {
		return nil, 0, fmt.Errorf("reading ARC32 schema: %w", err)
	}

	var err error
	if app.TSS, err = readLogicSig(resources, pathTo(tssTealFile)); err != nil {
		return nil, 0, fmt.Errorf("reading TSS: %w", err)
	}
	if app.DepositVerifier, err = readLogicSig(resources, pathTo(depositVerifierTealFile)); err != nil {
		return nil, 0, fmt.Errorf("reading DepositVerifier: %w", err)
	}
	if app.WithdrawalVerifier, err = readLogicSig(resources, pathTo(withdrawalVerifierTealFile)); err != nil {
		return nil, 0, fmt.Errorf("reading WithdrawalVerifier: %w", err)
	}
	if err := decodeJSONFile(resources, pathTo(treeConfigFile), &app.TreeConfig); err != nil {
		return nil, 0, fmt.Errorf("reading TreeConfig: %w", err)
	}
	app.TreeConfig.HashFunc = config.Hash

	// algoplonk/utils only supports reading from disk, so we must write embedded circuits to a temp dir.
	tempDir, err := os.MkdirTemp("", "hermes-circuits-*")
	if err != nil {
		return nil, 0, fmt.Errorf("creating temp dir for circuits: %w", err)
	}
	defer os.RemoveAll(tempDir)

	depositPath, err := writeToTemp(resources, circuitPathTo(compiledDepositCircuitFile), tempDir)
	if err != nil {
		return nil, 0, fmt.Errorf("writing deposit circuit to temp: %w", err)
	}
	if app.DepositCc, err = utils.DeserializeCompiledCircuit(depositPath); err != nil {
		return nil, 0, fmt.Errorf("deserializing deposit circuit: %w", err)
	}

	withdrawalPath, err := writeToTemp(resources, circuitPathTo(compiledWithdrawalCircuitFile), tempDir)
	if err != nil {
		return nil, 0, fmt.Errorf("writing withdrawal circuit to temp: %w", err)
	}
	if app.WithdrawalCc, err = utils.DeserializeCompiledCircuit(withdrawalPath); err != nil {
		return nil, 0, fmt.Errorf("deserializing withdrawal circuit: %w", err)
	}

	return app, aj.CreationBlock, nil
}

func writeToTemp(resources fs.FS, path string, tempDir string) (string, error) {
	data, err := fs.ReadFile(resources, path)
	if err != nil {
		return "", err
	}
	tempPath := filepath.Join(tempDir, filepath.Base(path))
	if err := os.WriteFile(tempPath, data, 0600); err != nil {
		return "", err
	}
	return tempPath, nil
}

func readLogicSig(resources fs.FS, compiledPath string) (*models.LSig, error) {
	bytecode, err := fs.ReadFile(resources, compiledPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", compiledPath, err)
	}
	lsigAccount, err := crypto.MakeLogicSigAccountEscrowChecked(bytecode, nil)
	if err != nil {
		return nil, fmt.Errorf("creating logic sig account: %w", err)
	}
	address, err := lsigAccount.Address()
	if err != nil {
		return nil, fmt.Errorf("getting lsig address: %w", err)
	}
	return &models.LSig{Account: lsigAccount, Address: address}, nil
}

func decodeJSONFile(resources fs.FS, path string, v any) error {
	f, err := resources.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(v); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func (c *Client) getMinimumBalance() uint64 {
	for attempt := 1; attempt <= 3; attempt++ {
		_, mbr, err := c.GetBalanceAndMBR(c.App.TSS.Address.String())
		if err == nil {
			return mbr
		}
		logger.Log("getting MBR (attempt %d/3): %v", attempt, err)
		if attempt < 3 {
			time.Sleep(time.Second)
		}
	}
	logger.Log("Warning: could not fetch MBR after 3 attempts; using 0")
	return 0
}
