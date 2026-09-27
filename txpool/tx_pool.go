// Copyright (c) 2020 The Meter.io developers

// Distributed under the GNU Lesser General Public License v3.0 software license, see the accompanying
// file LICENSE or <https://www.gnu.org/licenses/lgpl-3.0.html>

package txpool

import (
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cometbft/cometbft/v2/libs/bytes"
	cmttypes "github.com/cometbft/cometbft/v2/types"
	"github.com/ethereum/go-ethereum/event"
	lru "github.com/hashicorp/golang-lru"
	"github.com/meterio/supernova/chain"
	"github.com/meterio/supernova/libs/co"
	"github.com/meterio/supernova/types"
)

var (
	errTxExisted = errors.New("tx existed")
)

var DefaultTxPoolOptions = Options{
	Limit:           200000,
	LimitPerAccount: 1024, /*16,*/ //XXX: increase to 1024 from 16 during the testing
	MaxLifetime:     20 * time.Minute,
}

// Options options for tx pool.
type Options struct {
	Limit           int
	LimitPerAccount int
	MaxLifetime     time.Duration
}

// TxEvent will be posted when tx is added or status changed.
type TxEvent struct {
	Tx         cmttypes.Tx
	Executable *bool
}

// TxPool maintains unprocessed transactions.
type TxPool struct {
	options Options
	chain   *chain.Chain

	executables sync.Map // key: txId, value: txObject
	// ids of txs recently removed (included in an accepted proposal), so a late
	// copy from a peer or client is not re-added and proposed again
	recent *lru.Cache

	// validator checks txs from untrusted sources (Add, AddRemote); nil = none
	validator atomic.Pointer[func(cmttypes.Tx) error]
	// onLocalAdd is called for txs added via Add/AddChecked (e.g. to gossip them)
	onLocalAdd atomic.Pointer[func(cmttypes.Tx)]

	done   chan struct{}
	txFeed event.Feed
	scope  event.SubscriptionScope
	goes   co.Goes

	logger *slog.Logger
}

// New create a new TxPool instance.
// Shutdown is required to be called at end.
const recentlyRemovedSize = 200_000

func New(chain *chain.Chain, options Options) *TxPool {
	recent, err := lru.New(recentlyRemovedSize)
	if err != nil {
		panic(err)
	}
	pool := &TxPool{
		recent:      recent,
		options:     options,
		executables: sync.Map{},
		chain:       chain,
		done:        make(chan struct{}),

		logger: slog.With("pkg", "txpool"),
	}
	return pool
}

// Close cleanup inner go routines.
func (p *TxPool) Close() {
	close(p.done)
	p.scope.Close()
	p.goes.Wait()
	p.logger.Debug("closed")
}

// SubscribeTxEvent receivers will receive a tx
func (p *TxPool) SubscribeTxEvent(ch chan *TxEvent) event.Subscription {
	return p.scope.Track(p.txFeed.Subscribe(ch))
}

// SetValidator sets the check applied to txs from untrusted sources (Add and
// AddRemote), e.g. ABCI CheckTx.
func (p *TxPool) SetValidator(f func(cmttypes.Tx) error) {
	p.validator.Store(&f)
}

// SetOnLocalAdd sets a callback for txs accepted via Add or AddChecked.
func (p *TxPool) SetOnLocalAdd(f func(cmttypes.Tx)) {
	p.onLocalAdd.Store(&f)
}

// Add adds a tx submitted to this node (e.g. via the API): validated, then
// announced via the local-add callback.
func (p *TxPool) Add(newTx cmttypes.Tx) error {
	return p.add(newTx, true, true)
}

// AddChecked adds a tx the caller has already validated (e.g. with its own
// CheckTx), then announces it via the local-add callback.
func (p *TxPool) AddChecked(newTx cmttypes.Tx) error {
	return p.add(newTx, false, true)
}

// AddRemote adds a tx received from a peer: validated, not re-announced.
func (p *TxPool) AddRemote(newTx cmttypes.Tx) error {
	return p.add(newTx, true, false)
}

func (p *TxPool) add(newTx cmttypes.Tx, validate, local bool) error {
	txObj, err := resolveTx(newTx)
	if err != nil {
		return badTxError{err.Error()}
	}

	id := newTx.Hash().String()
	if _, ok := p.executables.Load(id); ok {
		return errTxExisted
	}
	if p.recent.Contains(id) {
		return errTxExisted
	}
	if validate {
		if v := p.validator.Load(); v != nil {
			if err := (*v)(newTx); err != nil {
				return badTxError{err.Error()}
			}
		}
	}
	if _, loaded := p.executables.LoadOrStore(id, txObj); loaded {
		return errTxExisted
	}
	// Validation can take a while; if the tx was included in a processed
	// proposal meanwhile, Remove ran before our store and couldn't delete it.
	// Remove records the id before deleting, so checking after storing closes
	// the race.
	if p.recent.Contains(id) {
		p.executables.Delete(id)
		return errTxExisted
	}

	p.logger.Debug("tx added", "id", newTx.Hash())
	if local {
		if f := p.onLocalAdd.Load(); f != nil {
			(*f)(newTx)
		}
	}
	p.goes.Go(func() {
		v := true
		p.txFeed.Send(&TxEvent{newTx, &v})
	})

	return nil
}

func (p *TxPool) Get(id []byte) cmttypes.Tx {
	strId := bytes.HexBytes(id).String()
	if txObj, ok := p.executables.Load(strId); ok {
		return txObj.(*txObject).Tx
	}
	return nil
}

// Remove removes tx from pool by its ID.
func (p *TxPool) Remove(id []byte) bool {
	strId := bytes.HexBytes(id).String()
	p.recent.Add(strId, struct{}{})
	if _, ok := p.executables.Load(strId); ok {
		p.executables.Delete(strId)
		hash := hex.EncodeToString(id)
		p.logger.Debug("tx removed", "id", hash)
		return true
	}
	return false
}

// FIXME: should sort tx
// Executables returns executable txs.
func (p *TxPool) Executables() types.Transactions {
	txList := make([]cmttypes.Tx, 0)
	p.executables.Range(func(key any, value any) bool {
		txList = append(txList, value.(*txObject).Tx)
		return true
	})
	return txList
}
