package consensus

import (
	"bytes"
	"testing"

	"github.com/OffchainLabs/prysm/v6/crypto/bls"
	cmttypes "github.com/cometbft/cometbft/v2/types"
	"github.com/meterio/supernova/block"
	"github.com/meterio/supernova/types"
)

func TestQCAndTCUseVotingPowerNotValidatorCount(t *testing.T) {
	masters := make([]*types.BlsMaster, 3)
	validators := make([]*cmttypes.Validator, 3)
	for i, power := range []int64{80, 10, 10} {
		masters[i] = types.NewBlsMasterWithRandKey()
		validators[i] = cmttypes.NewValidator(masters[i].CmtPubKey, power)
	}
	committee := cmttypes.NewValidatorSet(validators)
	voter := func(index int) *types.BlsMaster {
		for _, m := range masters {
			if bytes.Equal(m.CmtPubKey.Bytes(), committee.Validators[index].PubKey.Bytes()) {
				return m
			}
		}
		t.Fatal("validator key not found")
		return nil
	}
	parentID := types.Bytes32{0, 0, 0, 1}
	blk := new(block.Builder).ParentID(parentID).Build()
	id := blk.ID()
	timeoutHash := BuildTimeoutVotingHash(1, 3)
	qcMan := NewQCVoteManager(committee)
	tcMan := NewTCVoteManager(committee)
	var highIndex uint32
	for i, v := range committee.Validators {
		if v.VotingPower == 80 {
			highIndex = uint32(i)
			continue
		}
		master := voter(i)
		sig := master.SignMessage(id[:]).Marshal()
		parsed, err := bls.SignatureFromBytes(sig)
		if err != nil {
			t.Fatal(err)
		}
		if qc, _ := qcMan.AddVerifiedVote(uint32(i), v, 1, 3, id, parsed, nil, nil, nil, nil); qc != nil {
			t.Fatal("two 10-power validators formed a QC")
		}
		if tc := tcMan.AddVote(uint32(i), 1, 3, master.SignMessage(timeoutHash[:]).Marshal(), timeoutHash); tc != nil {
			t.Fatal("two 10-power validators formed a TC")
		}
	}
	master := voter(int(highIndex))
	parsed, err := bls.SignatureFromBytes(master.SignMessage(id[:]).Marshal())
	if err != nil {
		t.Fatal(err)
	}
	qc, _ := qcMan.AddVerifiedVote(highIndex, committee.Validators[highIndex], 1, 3, id, parsed, nil, nil, nil, nil)
	if qc == nil {
		t.Fatal("80-power validator did not complete a QC")
	}
	valid, err := blk.VerifyQC(qc, master, committee)
	if err != nil || !valid {
		t.Fatalf("weighted QC failed verification: valid=%v err=%v", valid, err)
	}
	if tc := tcMan.AddVote(highIndex, 1, 3, master.SignMessage(timeoutHash[:]).Marshal(), timeoutHash); tc == nil {
		t.Fatal("80-power validator did not complete a TC")
	}
	qc.BitArray.SetIndex(int(highIndex), false)
	if valid, _ := blk.VerifyQC(qc, master, committee); valid {
		t.Fatal("QC with only 20/100 voting power was accepted")
	}
}
