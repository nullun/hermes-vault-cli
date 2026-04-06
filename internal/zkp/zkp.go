// Package zkp provides zero-knowledge proof utilities.
package zkp

import (
	"fmt"

	"github.com/consensys/gnark/frontend"
	"github.com/giuliop/algoplonk"
	"github.com/giuliop/algoplonk/utils"
)

// ZKArgs generates and encodes a ZK proof as ABI-encoded arguments.
func ZKArgs(assignment frontend.Circuit, cc *algoplonk.CompiledCircuit) ([][]byte, error) {
	verifiedProof, err := cc.Verify(assignment)
	if err != nil {
		return nil, fmt.Errorf("failed to verify proof: %w", err)
	}
	proof := algoplonk.MarshalProof(verifiedProof.Proof)
	publicInputs, err := algoplonk.MarshalPublicInputs(verifiedProof.Witness)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal public inputs: %w", err)
	}
	zkArgs, err := utils.AbiEncodeProofAndPublicInputs(proof, publicInputs)
	if err != nil {
		return nil, fmt.Errorf("failed to abi encode proof and public inputs: %w", err)
	}
	return zkArgs, nil
}
