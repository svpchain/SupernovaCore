package consensus

import (
	"bytes"
	"testing"

	"github.com/OffchainLabs/prysm/v6/crypto/bls"
	v2 "github.com/cometbft/cometbft/api/cometbft/abci/v2"
	cmttypesv2 "github.com/cometbft/cometbft/api/cometbft/types/v2"
	cmttypes "github.com/cometbft/cometbft/v2/types"
	"github.com/meterio/supernova/block"
	"github.com/meterio/supernova/types"
)

func TestPreparedTxsPreserveApplicationInjectedOperations(t *testing.T) {
	// SVP's PrepareProposal injects operations and other application txs not
	// present in the mempool. Consensus must accept the returned sequence.
	operations := []byte("operations")
	ordinary := []byte("user-tx")
	prices := []byte("oracle")
	txs := [][]byte{operations, ordinary, prices}
	if err := validatePreparedTxs(txs, int64(len(operations)+len(ordinary)+len(prices))); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(txs[0], operations) || !bytes.Equal(txs[2], prices) {
		t.Fatal("application transaction order changed")
	}
	if err := validatePreparedTxs(txs, 10); err == nil {
		t.Fatal("oversized proposal accepted")
	}
	if err := validatePreparedTxs([][]byte{{}}, 10); err == nil {
		t.Fatal("empty transaction accepted")
	}
}

func TestExtendedCommitInfoIncludesAbsentValidatorsInOrder(t *testing.T) {
	masters := make([]*types.BlsMaster, 4)
	validators := make([]*cmttypes.Validator, 4)
	for i := range masters {
		masters[i] = types.NewBlsMasterWithRandKey()
		validators[i] = cmttypes.NewValidator(masters[i].CmtPubKey, 10)
	}
	committee := cmttypes.NewValidatorSet(validators)
	manager := NewQCVoteManager(committee)
	var blockID types.Bytes32
	blockID[0] = 1
	var qc *block.QuorumCert
	var info *v2.ExtendedCommitInfo
	for _, index := range []uint32{3, 1, 0} {
		// Sign with the key corresponding to the canonical validator index.
		var master *types.BlsMaster
		for _, candidate := range masters {
			if bytes.Equal(candidate.CmtPubKey.Bytes(), committee.Validators[index].PubKey.Bytes()) {
				master = candidate
				break
			}
		}
		if master == nil {
			t.Fatal("missing validator key")
		}
		var err error
		var signature bls.Signature
		signature, err = bls.SignatureFromBytes(master.SignMessage(blockID[:]).Marshal())
		if err != nil {
			t.Fatal(err)
		}
		qc, info = manager.AddVerifiedVote(index, committee.Validators[index], 1, 3, blockID, signature,
			[]byte{byte(index)}, []byte("signature"), nil, nil, nil)
	}
	if qc == nil || info == nil || len(info.Votes) != 4 {
		t.Fatalf("expected 4 ordered vote slots and QC, got QC=%v info=%v", qc, info)
	}
	for i, vote := range info.Votes {
		if !bytes.Equal(vote.Validator.Address, committee.Validators[i].Address) {
			t.Fatalf("validator address at slot %d does not match committee", i)
		}
		if i == 2 {
			if vote.BlockIdFlag != cmttypesv2.BlockIDFlagAbsent {
				t.Fatalf("absent validator marked present: %+v", vote)
			}
		} else if vote.BlockIdFlag != cmttypesv2.BlockIDFlagCommit || !bytes.Equal(vote.VoteExtension, []byte{byte(i)}) {
			t.Fatalf("vote extension for validator %d lost: %+v", i, vote)
		}
	}
}
