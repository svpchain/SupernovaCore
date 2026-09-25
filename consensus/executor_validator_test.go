package consensus

import (
	"strings"
	"testing"

	abci "github.com/cometbft/cometbft/v2/abci/types"
	"github.com/cometbft/cometbft/v2/crypto/bls12381"
	cmttypes "github.com/cometbft/cometbft/v2/types"
)

func TestCalcNewValidatorSetRejectsInvalidBLSKey(t *testing.T) {
	key, err := bls12381.GenPrivKey()
	if err != nil {
		t.Fatal(err)
	}
	current := cmttypes.NewValidatorSet([]*cmttypes.Validator{cmttypes.NewValidator(key.PubKey(), 10)})

	// Regression: the demo used to supply a 48-byte compressed BLS key, while
	// this CometBFT implementation deserializes 96-byte public keys.
	updated, err := calcNewValidatorSet(current, abci.ValidatorUpdates{{
		PubKeyType:  bls12381.KeyType,
		PubKeyBytes: make([]byte, 48),
		Power:       10,
	}}, nil)
	if updated != nil || err == nil || !strings.Contains(err.Error(), "48 bytes") {
		t.Fatalf("expected descriptive invalid-key error, got set=%v err=%v", updated, err)
	}
}
