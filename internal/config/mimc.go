package config

import (
	"fmt"
	"hash"

	"github.com/consensys/gnark-crypto/ecc"
	bls12_381mimc "github.com/consensys/gnark-crypto/ecc/bls12-381/fr/mimc"
	bn254mimc "github.com/consensys/gnark-crypto/ecc/bn254/fr/mimc"
)

// NewMiMC returns a MiMC hash function for the given curve.
func NewMiMC(curve ecc.ID) func(...[]byte) []byte {
	return func(data ...[]byte) []byte {
		return mimc(curve, data...)
	}
}

func mimc(curve ecc.ID, data ...[]byte) []byte {
	var m hash.Hash
	switch curve {
	case ecc.BN254:
		m = bn254mimc.NewMiMC()
	case ecc.BLS12_381:
		m = bls12_381mimc.NewMiMC()
	default:
		panic(fmt.Sprintf("mimc: unsupported curve: %v", curve))
	}
	input := make([]byte, 0, 32*len(data))
	for _, slice := range data {
		input = append(input, slice...)
	}
	_, err := m.Write(input)
	if err != nil {
		panic(err)
	}
	return m.Sum(nil)
}
