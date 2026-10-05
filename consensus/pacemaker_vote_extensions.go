package consensus

import (
	"fmt"
	"github.com/meterio/supernova/block"
	"math"
	"time"

	"github.com/OffchainLabs/prysm/v6/crypto/bls"
	v2 "github.com/cometbft/cometbft/api/cometbft/abci/v2"
	cmn "github.com/meterio/supernova/libs/common"
	"github.com/meterio/supernova/types"
)

func (p *Pacemaker) voteExtensionsEnabled(height uint32) bool {
	return p.voteExtensionEnableHeight > 0 && int64(height) >= p.voteExtensionEnableHeight
}

// verifyApplicationVoteExtension verifies the BLS signatures before passing
// validator-provided data to the application. This does not replace the vote
// signature verification performed by EpochState.AddQCVote.
func (p *Pacemaker) verifyApplicationVoteExtension(index uint32, blockID types.Bytes32, round uint32, extension, extensionSig, appSig, nonRpExtension, nonRpSignature []byte) bool {
	if !p.voteExtensionsEnabled(block.Number(blockID)) {
		return len(extension) == 0 && len(nonRpExtension) == 0
	}
	if index >= p.epochState.CommitteeSize() {
		return false
	}
	_, validator := p.epochState.GetValidatorByIndex(int(index))
	pubkey, err := cmn.PublicKeyFromBytes(validator.PubKey.Bytes())
	if err != nil {
		return false
	}
	sig, err := bls.SignatureFromBytes(extensionSig)
	if err != nil || !sig.Verify(pubkey, types.GetMsgHashForVoteExtension(p.epochState.epoch, blockID[:], extension)) {
		return false
	}
	nonRpSig, err := bls.SignatureFromBytes(nonRpSignature)
	if err != nil || !nonRpSig.Verify(pubkey, nonRpExtension) {
		return false
	}
	appAddr := validator.Address
	if p.appIdentities != nil {
		if round > math.MaxInt32 {
			return false
		}
		if !p.appIdentities.verifyExtension(validator, int64(block.Number(blockID)), int32(round), extension, appSig) {
			return false
		}
		identity, _ := p.appIdentities.forValidator(validator)
		appAddr = appAddress(identity.AppPubKey)
	}
	response, err := p.executor.VerifyVoteExtension(&v2.VerifyVoteExtensionRequest{
		Hash:               blockID[:],
		Height:             int64(block.Number(blockID)),
		ValidatorAddress:   appAddr,
		VoteExtension:      extension,
		NonRpVoteExtension: nonRpExtension,
	})
	if err != nil || response == nil || response.Status != v2.VERIFY_VOTE_EXTENSION_STATUS_ACCEPT {
		p.logger.Warn("application rejected vote extension", "height", block.Number(blockID), "validator", appAddr, "err", err)
		return false
	}
	return true
}

func (e *Executor) ExtendVoteForBlock(blk *block.Block) (*v2.ExtendVoteResponse, error) {
	parent, err := e.chain.GetBlock(blk.ParentID())
	if err != nil {
		parentDraft := e.chain.GetDraft(blk.ParentID())
		if parentDraft == nil || parentDraft.ProposedBlock == nil {
			return nil, fmt.Errorf("cannot extend vote for block %d: parent missing", blk.Number())
		}
		parent = parentDraft.ProposedBlock
	}
	vset := e.chain.GetValidatorsByHash(blk.ValidatorsHash())
	if vset == nil || int(blk.ProposerIndex()) >= vset.Size() {
		return nil, fmt.Errorf("cannot extend vote for block %d: validator set/proposer missing", blk.Number())
	}
	proposerAddress, proposer := vset.GetByIndex(int32(blk.ProposerIndex()))
	if e.appIdentities != nil {
		proposerAddress, err = e.appIdentities.appAddressFor(proposer)
		if err != nil {
			return nil, err
		}
	}
	lastCommit, err := e.lastCommitInfo(parent, blk)
	if err != nil {
		return nil, err
	}
	response, err := e.ExtendVote(&v2.ExtendVoteRequest{
		Hash:               blk.ID().Bytes(),
		Height:             int64(blk.Number()),
		Time:               time.Unix(0, int64(blk.NanoTimestamp())),
		Txs:                blk.Transactions().Convert(),
		ProposedLastCommit: lastCommit,
		NextValidatorsHash: blk.NextValidatorsHash(),
		ProposerAddress:    proposerAddress,
	})
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("ExtendVote returned nil response for block %d", blk.Number())
	}
	return response, nil
}
