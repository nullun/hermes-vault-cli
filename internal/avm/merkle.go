package avm

import (
	"bytes"
	"fmt"

	"github.com/nullun/hermes-vault-cli/internal/config"
	"github.com/nullun/hermes-vault-cli/internal/db"
)

// getRoot returns the Merkle root from the database, validating that leafIndex is in range.
func getRoot(leafIndex uint64) ([]byte, error) {
	root, leafCount, err := db.GetRoot()
	if err != nil {
		return nil, fmt.Errorf("error getting root: %w", err)
	}
	if leafIndex >= leafCount {
		return nil, fmt.Errorf("leaf index %d not in tree (leaf count: %d)", leafIndex, leafCount)
	}
	return root, nil
}

// createMerkleProof returns the Merkle proof path for the leaf at leafIndex.
// proof[0] is the leaf value (not hashed); subsequent entries are sibling hashes.
func (c *Client) createMerkleProof(leafValue []byte, leafIndex uint64, root []byte) ([][]byte, error) {
	depth := config.MerkleTreeLevels
	proof := make([][]byte, 1, depth+1)
	proof[0] = leafValue

	currentLevel, err := db.GetAllLeavesCommitments()
	if err != nil {
		return nil, fmt.Errorf("error getting all leaf commitments: %w", err)
	}
	if !bytes.Equal(config.Hash(leafValue), currentLevel[leafIndex]) {
		return nil, fmt.Errorf("leaf commitment mismatch at index %d", leafIndex)
	}
	if len(currentLevel)%2 == 1 {
		currentLevel = append(currentLevel, c.App.TreeConfig.ZeroHashes[0])
	}
	nextLevel := make([][]byte, (len(currentLevel)+1)/2)
	idx := leafIndex
	for i := 0; i < depth; i++ {
		if idx&1 == 0 {
			proof = append(proof, currentLevel[idx+1])
		} else {
			proof = append(proof, currentLevel[idx-1])
		}
		for j := 0; j < len(currentLevel); j += 2 {
			nextLevel[j/2] = config.Hash(currentLevel[j], currentLevel[j+1])
		}
		if len(nextLevel)%2 == 1 {
			nextLevel = append(nextLevel, c.App.TreeConfig.ZeroHashes[i+1])
		}
		currentLevel = nextLevel
		nextLevel = nextLevel[:len(nextLevel)/2]
		idx >>= 1
	}
	if len(nextLevel) != 1 || !bytes.Equal(nextLevel[0], root) {
		return nil, fmt.Errorf("merkle root mismatch")
	}
	return proof, nil
}
