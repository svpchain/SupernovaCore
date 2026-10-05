package consensus

import (
	"bytes"
	"context"
	"encoding/binary"
	"log/slog"
	"testing"

	v2 "github.com/cometbft/cometbft/api/cometbft/abci/v2"
	cmtproxy "github.com/cometbft/cometbft/v2/proxy"
	cmttypes "github.com/cometbft/cometbft/v2/types"
	"github.com/meterio/supernova/types"
)

type extensionConn struct {
	cmtproxy.AppConnConsensus
	status  v2.VerifyVoteExtensionStatus
	request *v2.VerifyVoteExtensionRequest
}

func (c *extensionConn) VerifyVoteExtension(_ context.Context, req *v2.VerifyVoteExtensionRequest) (*v2.VerifyVoteExtensionResponse, error) {
	c.request = req
	return &v2.VerifyVoteExtensionResponse{Status: c.status}, nil
}

func TestVerifyVoteExtensionCallsApplicationWithSignedData(t *testing.T) {
	master := types.NewBlsMasterWithRandKey()
	committee := cmttypes.NewValidatorSet([]*cmttypes.Validator{cmttypes.NewValidator(master.CmtPubKey, 10)})
	conn := &extensionConn{status: v2.VERIFY_VOTE_EXTENSION_STATUS_ACCEPT}
	p := &Pacemaker{
		logger:                    slog.Default(),
		epochState:                &EpochState{epoch: 1, committee: committee},
		executor:                  &Executor{proxyApp: conn},
		voteExtensionEnableHeight: 1,
	}
	var id types.Bytes32
	binary.BigEndian.PutUint32(id[:4], 1)
	ext := []byte("svp-price-vote")
	nonRp := []byte("extra")
	sig := master.SignMessage(types.GetMsgHashForVoteExtension(1, id[:], ext)).Marshal()
	nonRpSig := master.SignMessage(nonRp).Marshal()
	if !p.verifyApplicationVoteExtension(0, id, 0, ext, sig, nil, nonRp, nonRpSig) {
		t.Fatal("valid extension rejected")
	}
	if conn.request == nil || conn.request.Height != 1 ||
		!bytes.Equal(conn.request.ValidatorAddress, committee.Validators[0].Address) ||
		!bytes.Equal(conn.request.VoteExtension, ext) {
		t.Fatalf("wrong application VerifyVoteExtension request: %+v", conn.request)
	}
	conn.status = v2.VERIFY_VOTE_EXTENSION_STATUS_REJECT
	if p.verifyApplicationVoteExtension(0, id, 0, ext, sig, nil, nonRp, nonRpSig) {
		t.Fatal("application rejection ignored")
	}
	conn.request = nil
	if p.verifyApplicationVoteExtension(0, id, 0, ext, []byte("bad-signature"), nil, nonRp, nonRpSig) || conn.request != nil {
		t.Fatal("invalid signature reached application")
	}
}
