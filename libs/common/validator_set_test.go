package common

import (
	"testing"

	v2 "github.com/cometbft/cometbft/api/cometbft/abci/v2"
	"github.com/cometbft/cometbft/v2/crypto/bls12381"
	cmttypes "github.com/cometbft/cometbft/v2/types"
)

func TestApplyUpdatesToValidatorSet(t *testing.T) {
	key1, err := bls12381.GenPrivKey()
	if err != nil {
		t.Fatal(err)
	}
	key2, err := bls12381.GenPrivKey()
	if err != nil {
		t.Fatal(err)
	}
	original := cmttypes.NewValidatorSet([]*cmttypes.Validator{cmttypes.NewValidator(key1.PubKey(), 10)})

	unchanged := ApplyUpdatesToValidatorSet(original, nil)
	if unchanged.Size() != 1 || unchanged.Validators[0].VotingPower != 10 {
		t.Fatalf("empty updates must keep original validator: %+v", unchanged)
	}

	added := ApplyUpdatesToValidatorSet(original, []v2.ValidatorUpdate{{
		PubKeyType: key2.PubKey().Type(), PubKeyBytes: key2.PubKey().Bytes(), Power: 10,
	}})
	if added.Size() != 2 || original.Size() != 1 {
		t.Fatalf("adding validator must not mutate original: got %d, original %d", added.Size(), original.Size())
	}

	updated := ApplyUpdatesToValidatorSet(original, []v2.ValidatorUpdate{{
		PubKeyType: key1.PubKey().Type(), PubKeyBytes: key1.PubKey().Bytes(), Power: 20,
	}})
	if updated.Size() != 1 || updated.Validators[0].VotingPower != 20 || original.Validators[0].VotingPower != 10 {
		t.Fatalf("updating validator must not mutate original: got %+v, original %+v", updated, original)
	}

	removed := ApplyUpdatesToValidatorSet(added, []v2.ValidatorUpdate{{
		PubKeyType: key2.PubKey().Type(), PubKeyBytes: key2.PubKey().Bytes(), Power: 0,
	}})
	if removed.Size() != 1 || added.Size() != 2 {
		t.Fatalf("removing validator must not mutate input: got %d, original %d", removed.Size(), added.Size())
	}
}
