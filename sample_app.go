package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log"

	"github.com/cockroachdb/pebble"
	abcitypes "github.com/cometbft/cometbft/v2/abci/types"
)

var lastBlockHeightKey = []byte("\x00supernova/last-block-height")

type KVStoreApplication struct {
	db           *pebble.DB
	onGoingBatch *pebble.Batch
}

var _ abcitypes.Application = (*KVStoreApplication)(nil)

func NewKVStoreApplication(db *pebble.DB) *KVStoreApplication {
	return &KVStoreApplication{db: db}
}

func (app *KVStoreApplication) isValid(tx []byte) uint32 {
	// check format
	parts := bytes.Split(tx, []byte("="))
	if len(parts) != 2 {
		return 1
	}
	return 0
}

func (app *KVStoreApplication) Info(_ context.Context, info *abcitypes.InfoRequest) (*abcitypes.InfoResponse, error) {
	value, closer, err := app.db.Get(lastBlockHeightKey)
	if errors.Is(err, pebble.ErrNotFound) {
		return &abcitypes.InfoResponse{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	if len(value) != 8 {
		return nil, errors.New("invalid persisted application height")
	}
	return &abcitypes.InfoResponse{LastBlockHeight: int64(binary.BigEndian.Uint64(value))}, nil
}

func (app *KVStoreApplication) Query(_ context.Context, req *abcitypes.QueryRequest) (*abcitypes.QueryResponse, error) {
	resp := abcitypes.QueryResponse{Key: req.Data}

	value, closer, err := app.db.Get(req.Data)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			resp.Log = "key does not exist"
		} else {
			log.Panicf("Error reading database, unable to execute query: %v", err)
		}
		return &resp, nil
	}
	defer closer.Close()

	resp.Log = "exists"
	resp.Value = value
	return &resp, nil
}

func (app *KVStoreApplication) CheckTx(_ context.Context, check *abcitypes.CheckTxRequest) (*abcitypes.CheckTxResponse, error) {
	code := app.isValid(check.Tx)
	return &abcitypes.CheckTxResponse{Code: code}, nil
}

func (app *KVStoreApplication) InitChain(_ context.Context, chain *abcitypes.InitChainRequest) (*abcitypes.InitChainResponse, error) {
	// The genesis builder uses InitChain validators to construct the first committee.
	return &abcitypes.InitChainResponse{Validators: chain.Validators}, nil
}

func (app *KVStoreApplication) PrepareProposal(_ context.Context, proposal *abcitypes.PrepareProposalRequest) (*abcitypes.PrepareProposalResponse, error) {
	return &abcitypes.PrepareProposalResponse{Txs: proposal.Txs}, nil
}

func (app *KVStoreApplication) ProcessProposal(_ context.Context, proposal *abcitypes.ProcessProposalRequest) (*abcitypes.ProcessProposalResponse, error) {
	return &abcitypes.ProcessProposalResponse{Status: abcitypes.PROCESS_PROPOSAL_STATUS_ACCEPT}, nil
}

func (app *KVStoreApplication) FinalizeBlock(_ context.Context, req *abcitypes.FinalizeBlockRequest) (*abcitypes.FinalizeBlockResponse, error) {
	var txs = make([]*abcitypes.ExecTxResult, len(req.Txs))
	var updates = make([]abcitypes.ValidatorUpdate, 0)
	var events = make([]abcitypes.Event, 0)

	app.onGoingBatch = app.db.NewBatch()
	defer app.onGoingBatch.Close()

	for i, tx := range req.Txs {
		if code := app.isValid(tx); code != 0 {
			log.Printf("Error: invalid transaction index %v", i)
			txs[i] = &abcitypes.ExecTxResult{Code: code}
		} else {
			parts := bytes.SplitN(tx, []byte("="), 2)
			key, value := parts[0], parts[1]
			log.Printf("Adding key %s with value %s", key, value)

			if err := app.onGoingBatch.Set(key, value, pebble.Sync); err != nil {
				log.Panicf("Error writing to database, unable to execute tx: %v", err)
			}

			log.Printf("Successfully added key %s with value %s", key, value)

			txs[i] = &abcitypes.ExecTxResult{
				Code: 0,
				Events: []abcitypes.Event{
					{
						Type: "app",
						Attributes: []abcitypes.EventAttribute{
							{Key: "key", Value: string(key), Index: true},
							{Key: "value", Value: string(value), Index: true},
						},
					},
				},
			}
		}
	}

	// Keep the single-node demo's validator set unchanged. Adding a second
	// validator here would require a second running node to reach quorum.

	var heightBytes [8]byte
	binary.BigEndian.PutUint64(heightBytes[:], uint64(req.Height))
	if err := app.onGoingBatch.Set(lastBlockHeightKey, heightBytes[:], pebble.NoSync); err != nil {
		return nil, err
	}

	if err := app.onGoingBatch.Commit(pebble.Sync); err != nil {
		log.Panicf("Failed to commit batch: %v", err)
	}

	return &abcitypes.FinalizeBlockResponse{
		TxResults:        txs,
		ValidatorUpdates: updates,
		Events:           events,
	}, nil
}

func (app *KVStoreApplication) Commit(_ context.Context, commit *abcitypes.CommitRequest) (*abcitypes.CommitResponse, error) {
	// PebbleDB batches are committed in FinalizeBlock, so nothing to do here
	return &abcitypes.CommitResponse{}, nil
}

func (app *KVStoreApplication) ListSnapshots(_ context.Context, snapshots *abcitypes.ListSnapshotsRequest) (*abcitypes.ListSnapshotsResponse, error) {
	return &abcitypes.ListSnapshotsResponse{}, nil
}

func (app *KVStoreApplication) OfferSnapshot(_ context.Context, snapshot *abcitypes.OfferSnapshotRequest) (*abcitypes.OfferSnapshotResponse, error) {
	return &abcitypes.OfferSnapshotResponse{}, nil
}

func (app *KVStoreApplication) LoadSnapshotChunk(_ context.Context, chunk *abcitypes.LoadSnapshotChunkRequest) (*abcitypes.LoadSnapshotChunkResponse, error) {
	return &abcitypes.LoadSnapshotChunkResponse{}, nil
}

func (app *KVStoreApplication) ApplySnapshotChunk(_ context.Context, chunk *abcitypes.ApplySnapshotChunkRequest) (*abcitypes.ApplySnapshotChunkResponse, error) {
	return &abcitypes.ApplySnapshotChunkResponse{Result: abcitypes.APPLY_SNAPSHOT_CHUNK_RESULT_ACCEPT}, nil
}

func (app *KVStoreApplication) ExtendVote(_ context.Context, extend *abcitypes.ExtendVoteRequest) (*abcitypes.ExtendVoteResponse, error) {
	return &abcitypes.ExtendVoteResponse{}, nil
}

func (app *KVStoreApplication) VerifyVoteExtension(_ context.Context, verify *abcitypes.VerifyVoteExtensionRequest) (*abcitypes.VerifyVoteExtensionResponse, error) {
	return &abcitypes.VerifyVoteExtensionResponse{}, nil
}
