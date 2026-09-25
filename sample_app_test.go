package main

import (
	"context"
	"testing"

	"github.com/cockroachdb/pebble"
	abci "github.com/cometbft/cometbft/v2/abci/types"
)

func TestKVStoreApplicationInitAndRecoverHeight(t *testing.T) {
	db, err := pebble.Open(t.TempDir(), &pebble.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	app := NewKVStoreApplication(db)
	ctx := context.Background()
	info, err := app.Info(ctx, &abci.InfoRequest{})
	if err != nil || info.LastBlockHeight != 0 {
		t.Fatalf("initial Info: response=%+v err=%v", info, err)
	}

	validators := abci.ValidatorUpdates{{Power: 10}}
	initRes, err := app.InitChain(ctx, &abci.InitChainRequest{Validators: validators})
	if err != nil || len(initRes.Validators) != 1 || initRes.Validators[0].Power != 10 {
		t.Fatalf("InitChain: response=%+v err=%v", initRes, err)
	}

	res, err := app.FinalizeBlock(ctx, &abci.FinalizeBlockRequest{Height: 1, Txs: [][]byte{[]byte("hello=world")}})
	if err != nil || len(res.TxResults) != 1 || res.TxResults[0].Code != 0 {
		t.Fatalf("FinalizeBlock: response=%+v err=%v", res, err)
	}

	// Simulate a new application instance reading the same persisted database.
	restarted := NewKVStoreApplication(db)
	info, err = restarted.Info(ctx, &abci.InfoRequest{})
	if err != nil || info.LastBlockHeight != 1 {
		t.Fatalf("restarted Info: response=%+v err=%v", info, err)
	}
	query, err := restarted.Query(ctx, &abci.QueryRequest{Data: []byte("hello")})
	if err != nil || string(query.Value) != "world" {
		t.Fatalf("restarted Query: response=%+v err=%v", query, err)
	}
}

func TestKVStoreApplicationDoesNotInjectUnconfiguredValidator(t *testing.T) {
	db, err := pebble.Open(t.TempDir(), &pebble.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	app := NewKVStoreApplication(db)
	for _, height := range []int64{6, 20} {
		response, err := app.FinalizeBlock(context.Background(), &abci.FinalizeBlockRequest{Height: height})
		if err != nil {
			t.Fatalf("FinalizeBlock(%d): %v", height, err)
		}
		if len(response.ValidatorUpdates) != 0 {
			t.Fatalf("unexpected validator update at height %d: %+v", height, response.ValidatorUpdates)
		}
	}
}
