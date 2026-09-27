package node

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	cmttypes "github.com/cometbft/cometbft/v2/types"
	pubsub "github.com/libp2p/go-libp2p-pubsub"

	"github.com/meterio/supernova/libs/p2p"
	"github.com/meterio/supernova/txpool"
)

// txGossip propagates locally submitted txs to peers over p2p.TxTopic and
// adds txs received from peers to the pool (validated, not re-gossiped;
// gossipsub already delivers each message to every subscriber).
//
// Txs are batched: one pubsub message per txGossipFlushInterval, or sooner
// when a batch reaches txGossipMaxBatchTxs / txGossipMaxBatchBytes.
type txGossip struct {
	p2pSrv p2p.P2P
	pool   *txpool.TxPool
	queue  chan cmttypes.Tx
	logger *slog.Logger
}

const (
	txGossipFlushInterval = 20 * time.Millisecond
	txGossipMaxBatchTxs   = 1000
	txGossipMaxBatchBytes = 512 * 1024
	txGossipQueueSize     = 100_000

	// decode limits, well under pubsub's max message size
	txBatchMaxTxs    = 100_000
	txBatchMaxTxSize = 1 << 20
)

func newTxGossip(p2pSrv p2p.P2P, pool *txpool.TxPool) *txGossip {
	return &txGossip{
		p2pSrv: p2pSrv,
		pool:   pool,
		queue:  make(chan cmttypes.Tx, txGossipQueueSize),
		logger: slog.With("pkg", "txgossip"),
	}
}

// enqueue schedules a tx for gossip; it never blocks consensus or RPC paths.
func (g *txGossip) enqueue(tx cmttypes.Tx) {
	select {
	case g.queue <- tx:
	default:
		g.logger.Warn("tx gossip queue full, not gossiping tx", "id", tx.Hash())
	}
}

func (g *txGossip) publishLoop(ctx context.Context) {
	topic, err := g.p2pSrv.JoinTopic(p2p.TxTopic)
	if err != nil {
		g.logger.Error("join tx topic failed, tx gossip disabled", "err", err)
		return
	}
	ticker := time.NewTicker(txGossipFlushInterval)
	defer ticker.Stop()

	var batch []cmttypes.Tx
	size := 0
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := topic.Publish(ctx, encodeTxBatch(batch)); err != nil && ctx.Err() == nil {
			g.logger.Warn("publish tx batch failed", "txs", len(batch), "err", err)
		}
		batch, size = batch[:0], 0
	}
	for {
		select {
		case <-ctx.Done():
			return
		case tx := <-g.queue:
			batch = append(batch, tx)
			size += len(tx)
			if len(batch) >= txGossipMaxBatchTxs || size >= txGossipMaxBatchBytes {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (g *txGossip) receiveLoop(ctx context.Context) {
	sub, err := g.p2pSrv.SubscribeToTopic(p2p.TxTopic)
	if err != nil {
		g.logger.Error("subscribe to tx topic failed, not receiving gossiped txs", "err", err)
		return
	}
	defer sub.Cancel()
	self := g.p2pSrv.PeerID()
	workers := runtime.NumCPU()

	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			if !errors.Is(err, pubsub.ErrSubscriptionCancelled) && ctx.Err() == nil {
				g.logger.Error("tx subscription failed", "err", err)
			}
			return
		}
		if msg.ReceivedFrom == self {
			continue // our own publish, delivered locally
		}
		txs, err := decodeTxBatch(msg.Data)
		if err != nil {
			g.logger.Warn("malformed tx batch", "from", msg.ReceivedFrom, "err", err)
			continue
		}
		g.addRemote(txs, workers)
	}
}

// addRemote validates (via the pool's validator) and adds txs in parallel.
func (g *txGossip) addRemote(txs []cmttypes.Tx, workers int) {
	jobs := make(chan cmttypes.Tx)
	var wg sync.WaitGroup
	for i := 0; i < workers && i < len(txs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for tx := range jobs {
				_ = g.pool.AddRemote(tx) // duplicates and invalid txs are expected
			}
		}()
	}
	for _, tx := range txs {
		jobs <- tx
	}
	close(jobs)
	wg.Wait()
}

// encodeTxBatch: uvarint(count), then uvarint(len) || tx for each tx.
func encodeTxBatch(txs []cmttypes.Tx) []byte {
	size := binary.MaxVarintLen64
	for _, tx := range txs {
		size += binary.MaxVarintLen64 + len(tx)
	}
	buf := make([]byte, 0, size)
	buf = binary.AppendUvarint(buf, uint64(len(txs)))
	for _, tx := range txs {
		buf = binary.AppendUvarint(buf, uint64(len(tx)))
		buf = append(buf, tx...)
	}
	return buf
}

func decodeTxBatch(data []byte) ([]cmttypes.Tx, error) {
	n, k := binary.Uvarint(data)
	if k <= 0 || n > txBatchMaxTxs {
		return nil, fmt.Errorf("bad tx count")
	}
	data = data[k:]
	txs := make([]cmttypes.Tx, 0, n)
	for i := uint64(0); i < n; i++ {
		l, k := binary.Uvarint(data)
		if k <= 0 || l > txBatchMaxTxSize || uint64(len(data)-k) < l {
			return nil, fmt.Errorf("bad tx %d length", i)
		}
		data = data[k:]
		txs = append(txs, cmttypes.Tx(data[:l]))
		data = data[l:]
	}
	if len(data) != 0 {
		return nil, fmt.Errorf("%d trailing bytes", len(data))
	}
	return txs, nil
}
