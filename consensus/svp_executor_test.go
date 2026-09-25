package consensus

import (
	"bytes"
	"context"
	"testing"
	"time"

	cmtdb "github.com/cometbft/cometbft-db"
	v2 "github.com/cometbft/cometbft/api/cometbft/abci/v2"
	cmtproxy "github.com/cometbft/cometbft/v2/proxy"
	cmttypes "github.com/cometbft/cometbft/v2/types"
	"github.com/meterio/supernova/block"
	"github.com/meterio/supernova/chain"
	"github.com/meterio/supernova/genesis"
	"github.com/meterio/supernova/txpool"
	"github.com/meterio/supernova/types"
)

type proposalConn struct {
	cmtproxy.AppConnConsensus
	request *v2.PrepareProposalRequest
}

func (c *proposalConn) PrepareProposal(_ context.Context, req *v2.PrepareProposalRequest) (*v2.PrepareProposalResponse, error) {
	c.request = req
	return &v2.PrepareProposalResponse{Txs: [][]byte{[]byte("app-injected-operations"), []byte("app-injected-prices")}}, nil
}

func TestPrepareProposalForwardsVoteExtensionsAndApplicationTxs(t *testing.T) {
	master := types.NewBlsMasterWithRandKey()
	db := cmtdb.NewMemDB()
	c, err := chain.New(db, false)
	if err != nil {
		t.Fatal(err)
	}
	gene := genesis.NewGenesis(&cmttypes.GenesisDoc{
		GenesisTime: time.Unix(1710000000, 0),
		ChainID:     "1001",
		Validators:  []cmttypes.GenesisValidator{{Address: master.CmtPubKey.Address(), PubKey: master.CmtPubKey, Power: 10}},
	}, nil)
	if err := c.Initialize(gene); err != nil {
		t.Fatal(err)
	}
	pool := txpool.New(c, txpool.DefaultTxPoolOptions)
	defer pool.Close()
	conn := new(proposalConn)
	proposalTime := time.Unix(1710000001, 123)
	commit := &v2.ExtendedCommitInfo{Round: 7, Votes: []v2.ExtendedVoteInfo{{VoteExtension: []byte("oracle")}}}
	operations := []byte("app-injected-operations")
	exec := NewExecutor(conn, c, pool)
	prepared, err := exec.PrepareProposal(&block.DraftBlock{Height: 0, ProposedBlock: c.BestBlock()}, 0, 8, commit, proposalTime)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Txs) != 2 || !bytes.Equal(prepared.Txs[0], operations) {
		t.Fatalf("application-injected operations were lost or reordered: %q", prepared.Txs)
	}
	if conn.request == nil || conn.request.Height != 1 || !conn.request.Time.Equal(proposalTime) ||
		conn.request.LocalLastCommit.Round != 7 || len(conn.request.LocalLastCommit.Votes) != 1 ||
		!bytes.Equal(conn.request.LocalLastCommit.Votes[0].VoteExtension, []byte("oracle")) {
		t.Fatalf("wrong PrepareProposal request: %+v", conn.request)
	}
}
