// Copyright (C) 2019-2021, Lux Partners Limited. All rights reserved.
// See the file LICENSE for licensing terms.

// Package vm implements custom VM.
package vm

import (
	"context"
	ejson "encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/rpc/v2"

	log "github.com/inconshreveable/log15"

	"github.com/skychains/chain/cache"
	"github.com/skychains/chain/database"
	"github.com/skychains/chain/database/manager"
	"github.com/skychains/chain/database/versiondb"
	"github.com/skychains/chain/ids"
	"github.com/skychains/chain/snow"
	"github.com/skychains/chain/snow/choices"
	"github.com/skychains/chain/snow/consensus/snowman"
	"github.com/skychains/chain/snow/engine/common"
	snowmanblock "github.com/skychains/chain/snow/engine/snowman/block"
	"github.com/skychains/chain/utils"
	"github.com/skychains/chain/utils/json"
	luxdversion "github.com/skychains/chain/version"
	"github.com/skychains/spacesvm/chain"
	"github.com/skychains/spacesvm/mempool"
	"github.com/skychains/spacesvm/version"
)

const (
	Name           = "spacesvm"
	PublicEndpoint = "/public"
)

var (
	_ snowmanblock.ChainVM = &VM{}
	_ chain.VM             = &VM{}
)

type VM struct {
	ctx         *snow.Context
	db          database.Database
	config      Config
	genesis     *chain.Genesis
	AirdropData []byte

	bootstrapped utils.Atomic[bool]

	mempool   *mempool.Mempool
	appSender common.AppSender
	network   *PushNetwork

	// cache block objects to optimize "GetBlockStateless"
	// only put when a block is accepted
	// key: block ID, value: *chain.StatelessBlock
	blocks *cache.LRU[ids.ID, *chain.StatelessBlock]

	// Block ID --> Block
	// Each element is a block that passed verification but
	// hasn't yet been accepted/rejected
	verifiedBlocks map[ids.ID]*chain.StatelessBlock

	toEngine chan<- common.Message
	builder  BlockBuilder

	preferred    ids.ID
	lastAccepted *chain.StatelessBlock

	// Recent activity
	activityCacheCursor uint64
	activityCache       []*chain.Activity

	// Execution checks
	targetRangeUnits uint64

	stop chan struct{}

	builderStop chan struct{}
	doneBuild   chan struct{}
	doneGossip  chan struct{}
	donePrune   chan struct{}
	doneCompact chan struct{}
}

const (
	blocksLRUSize = 128
)

// implements "snowmanblock.ChainVM.common.VM"
func (vm *VM) Initialize(
	ctx context.Context,
	chainCtx *snow.Context,
	dbManager manager.Manager,
	genesisBytes []byte,
	upgradeBytes []byte,
	configBytes []byte,
	toEngine chan<- common.Message,
	_ []*common.Fx,
	appSender common.AppSender,
) error {
	log.Info("initializing spacesvm", "version", version.Version)

	// Load config
	vm.config.SetDefaults()
	if len(configBytes) > 0 {
		if err := ejson.Unmarshal(configBytes, &vm.config); err != nil {
			return fmt.Errorf("failed to unmarshal config %s: %w", string(configBytes), err)
		}
	}

	vm.ctx = chainCtx
	vm.db = dbManager.Current().Database
	vm.activityCache = make([]*chain.Activity, vm.config.ActivityCacheSize)

	// Init channels before initializing other structs
	vm.stop = make(chan struct{})
	vm.builderStop = make(chan struct{})
	vm.doneBuild = make(chan struct{})
	vm.doneGossip = make(chan struct{})
	vm.donePrune = make(chan struct{})
	vm.doneCompact = make(chan struct{})

	vm.appSender = appSender
	vm.network = vm.NewPushNetwork()

	vm.blocks = &cache.LRU[ids.ID, *chain.StatelessBlock]{Size: blocksLRUSize}
	vm.verifiedBlocks = make(map[ids.ID]*chain.StatelessBlock)

	vm.toEngine = toEngine
	vm.builder = vm.NewTimeBuilder()

	// Try to load last accepted
	has, err := chain.HasLastAccepted(vm.db)
	if err != nil {
		log.Error("could not determine if have last accepted")
		return err
	}

	// Parse genesis data
	vm.genesis = new(chain.Genesis)
	if err := ejson.Unmarshal(genesisBytes, vm.genesis); err != nil {
		log.Error("could not unmarshal genesis bytes")
		return err
	}
	if err := vm.genesis.Verify(); err != nil {
		log.Error("genesis is invalid")
		return err
	}
	targetUnitsPerSecond := vm.genesis.TargetBlockSize / uint64(vm.genesis.TargetBlockRate)
	vm.targetRangeUnits = targetUnitsPerSecond * uint64(vm.genesis.LookbackWindow)
	log.Debug("loaded genesis", "genesis", string(genesisBytes), "target range units", vm.targetRangeUnits)

	vm.mempool = mempool.New(vm.genesis, vm.config.MempoolSize)

	if has { //nolint:nestif
		blkID, err := chain.GetLastAccepted(vm.db)
		if err != nil {
			log.Error("could not get last accepted", "err", err)
			return err
		}

		blk, err := vm.GetStatelessBlock(blkID)
		if err != nil {
			log.Error("could not load last accepted", "err", err)
			return err
		}

		vm.preferred, vm.lastAccepted = blkID, blk
		log.Info("initialized spacesvm from last accepted", "block", blkID)
	} else {
		genesisBlk, err := chain.ParseStatefulBlock(
			vm.genesis.StatefulBlock(),
			nil,
			choices.Accepted,
			vm,
		)
		if err != nil {
			log.Error("unable to init genesis block", "err", err)
			return err
		}

		// Set Balances
		if err := vm.genesis.Load(vm.db, vm.AirdropData); err != nil {
			log.Error("could not set genesis allocation", "err", err)
			return err
		}

		if err := chain.SetLastAccepted(vm.db, genesisBlk); err != nil {
			log.Error("could not set genesis as last accepted", "err", err)
			return err
		}
		gBlkID := genesisBlk.ID()
		vm.preferred, vm.lastAccepted = gBlkID, genesisBlk
		log.Info("initialized spacesvm from genesis", "block", gBlkID)
	}
	vm.AirdropData = nil

	go vm.builder.Build()
	go vm.builder.Gossip()
	go vm.prune()
	go vm.compact()
	return nil
}

func (vm *VM) SetState(ctx context.Context, state snow.State) error {
	switch state {
	case snow.Bootstrapping:
		return vm.onBootstrapStarted()
	case snow.NormalOp:
		return vm.onNormalOperationsStarted()
	default:
		return snow.ErrUnknownState
	}
}

// onBootstrapStarted marks this VM as bootstrapping
func (vm *VM) onBootstrapStarted() error {
	vm.bootstrapped.Set(false)
	return nil
}

// onNormalOperationsStarted marks this VM as bootstrapped
func (vm *VM) onNormalOperationsStarted() error {
	if vm.bootstrapped.Get() {
		return nil
	}
	vm.bootstrapped.Set(true)
	return nil
}

// implements "snowmanblock.ChainVM.common.VM"
func (vm *VM) Shutdown(ctx context.Context) error {
	close(vm.stop)
	<-vm.doneBuild
	<-vm.doneGossip
	<-vm.donePrune
	<-vm.doneCompact
	if vm.ctx == nil {
		return nil
	}
	return vm.db.Close()
}

// implements "snowmanblock.ChainVM.common.VM"
func (vm *VM) Version(ctx context.Context) (string, error) { return version.Version.String(), nil }

// NewHandler returns a new Handler for a service where:
//   * The handler's functionality is defined by [service]
//     [service] should be a gorilla RPC service (see https://www.gorillatoolkit.org/pkg/rpc/v2)
//   * The name of the service is [name]
//   * The LockOption is the first element of [lockOption]
//     By default the LockOption is WriteLock
//     [lockOption] should have either 0 or 1 elements. Elements beside the first are ignored.
func newHandler(name string, service interface{}, lockOption ...common.LockOption) (*common.HTTPHandler, error) {
	server := rpc.NewServer()
	server.RegisterCodec(json.NewCodec(), "application/json")
	server.RegisterCodec(json.NewCodec(), "application/json;charset=UTF-8")
	if err := server.RegisterService(service, name); err != nil {
		return nil, err
	}

	var lock common.LockOption = common.WriteLock
	if len(lockOption) != 0 {
		lock = lockOption[0]
	}
	return &common.HTTPHandler{LockOptions: lock, Handler: server}, nil
}

// implements "snowmanblock.ChainVM.common.VM"
// for "ext/vm/[chainID]"
func (vm *VM) CreateHandlers(ctx context.Context) (map[string]*common.HTTPHandler, error) {
	apis := map[string]*common.HTTPHandler{}
	public, err := newHandler(Name, &PublicService{vm: vm})
	if err != nil {
		return nil, err
	}
	apis[PublicEndpoint] = public
	return apis, nil
}

// implements "snowmanblock.ChainVM.common.VM"
// for "ext/vm/[vmID]"
func (vm *VM) CreateStaticHandlers(ctx context.Context) (map[string]*common.HTTPHandler, error) {
	return nil, nil
}

// implements "snowmanblock.ChainVM.commom.VM.AppHandler"
func (vm *VM) AppRequest(ctx context.Context, nodeID ids.NodeID, requestID uint32, deadline time.Time, request []byte) error {
	// (currently) no app-specific messages
	return nil
}

// implements "snowmanblock.ChainVM.commom.VM.AppHandler"
func (vm *VM) AppRequestFailed(ctx context.Context, nodeID ids.NodeID, requestID uint32) error {
	// (currently) no app-specific messages
	return nil
}

// implements "snowmanblock.ChainVM.commom.VM.AppHandler"
func (vm *VM) AppResponse(ctx context.Context, nodeID ids.NodeID, requestID uint32, response []byte) error {
	// (currently) no app-specific messages
	return nil
}

// implements "snowmanblock.ChainVM.commom.VM.health.Checkable"
func (vm *VM) HealthCheck(ctx context.Context) (interface{}, error) {
	return http.StatusOK, nil
}

// implements "snowmanblock.ChainVM.commom.VM.validators.Connector"
func (vm *VM) Connected(ctx context.Context, id ids.NodeID, nodeVersion *luxdversion.Application) error {
	// no-op
	return nil
}

// implements "snowmanblock.ChainVM.commom.VM.validators.Connector"
func (vm *VM) Disconnected(ctx context.Context, id ids.NodeID) error {
	// no-op
	return nil
}

// implements "snowmanblock.ChainVM.commom.VM.Getter"
// replaces "core.SnowmanVM.GetBlock"
func (vm *VM) GetBlock(ctx context.Context, id ids.ID) (snowman.Block, error) {
	b, err := vm.GetStatelessBlock(id)
	if err != nil {
		log.Warn("failed to get block", "err", err)
	}
	return b, err
}

func (vm *VM) GetStatelessBlock(blkID ids.ID) (*chain.StatelessBlock, error) {
	// has the block been cached from previous "Accepted" call
	blk, exist := vm.blocks.Get(blkID)
	if exist {
		return blk, nil
	}

	// has the block been verified, not yet accepted
	if blk, exists := vm.verifiedBlocks[blkID]; exists {
		return blk, nil
	}

	// not found in memory, fetch from disk if accepted
	stBlk, err := chain.GetBlock(vm.db, blkID)
	if err != nil {
		return nil, err
	}
	// If block on disk, it must've been accepted
	return chain.ParseStatefulBlock(stBlk, nil, choices.Accepted, vm)
}

// implements "snowmanblock.ChainVM.commom.VM.Parser"
// replaces "core.SnowmanVM.ParseBlock"
func (vm *VM) ParseBlock(ctx context.Context, source []byte) (snowman.Block, error) {
	newBlk, err := chain.ParseBlock(
		source,
		choices.Processing,
		vm,
	)
	if err != nil {
		log.Error("could not parse block", "err", err)
		return nil, err
	}
	log.Debug("parsed block", "id", newBlk.ID())

	// If we have seen this block before, return it with the most
	// up-to-date info
	if oldBlk, err := vm.GetBlock(ctx, newBlk.ID()); err == nil {
		log.Debug("returning previously parsed block", "id", oldBlk.ID())
		return oldBlk, nil
	}

	return newBlk, nil
}

// implements "snowmanblock.ChainVM"
// called via "node" node over RPC
func (vm *VM) BuildBlock(ctx context.Context) (snowman.Block, error) {
	log.Debug("BuildBlock triggered")
	blk, err := chain.BuildBlock(vm, vm.preferred)
	vm.builder.HandleGenerateBlock()
	if err != nil {
		log.Debug("BuildBlock failed", "error", err)
		return nil, err
	}
	sblk, ok := blk.(*chain.StatelessBlock)
	if !ok {
		return nil, fmt.Errorf("unexpected snowman.Block %T, expected *StatelessBlock", blk)
	}

	log.Debug("BuildBlock success", "blkID", blk.ID(), "txs", len(sblk.Txs))
	return blk, nil
}

func (vm *VM) Submit(txs ...*chain.Transaction) (errs []error) {
	blk, err := vm.GetStatelessBlock(vm.preferred)
	if err != nil {
		return []error{err}
	}
	now := time.Now().Unix()
	ctx, err := vm.ExecutionContext(now, blk)
	if err != nil {
		return []error{err}
	}
	vdb := versiondb.New(vm.db)

	// Expire outdated spaces before checking submission validity
	if err := chain.ExpireNext(vdb, blk.Tmstmp, now, true); err != nil {
		return []error{err}
	}

	for _, tx := range txs {
		if err := vm.submit(tx, vdb, now, ctx); err != nil {
			log.Debug("failed to submit transaction",
				"tx", tx.ID(),
				"error", err,
			)
			errs = append(errs, err)
			continue
		}
		vdb.Abort()
	}
	return errs
}

func (vm *VM) submit(tx *chain.Transaction, db database.Database, blkTime int64, ctx *chain.Context) error {
	if err := tx.Init(vm.genesis); err != nil {
		return err
	}
	if err := tx.ExecuteBase(vm.genesis); err != nil {
		return err
	}
	dummy := chain.DummyBlock(blkTime, tx)
	if err := tx.Execute(vm.genesis, db, dummy, ctx); err != nil {
		return err
	}
	vm.mempool.Add(tx)
	return nil
}

// "SetPreference" implements "snowmanblock.ChainVM"
// replaces "core.SnowmanVM.SetPreference"
func (vm *VM) SetPreference(ctx context.Context, id ids.ID) error {
	log.Debug("set preference", "id", id)
	vm.preferred = id
	return nil
}

// "LastAccepted" implements "snowmanblock.ChainVM"
// replaces "core.SnowmanVM.LastAccepted"
func (vm *VM) LastAccepted(ctx context.Context) (ids.ID, error) {
	return vm.lastAccepted.ID(), nil
}

func (vm *VM) CrossChainAppRequest(ctx context.Context, chainID ids.ID, requestID uint32, deadline time.Time, request []byte) error {
	return nil
}

func (vm *VM) CrossChainAppRequestFailed(ctx context.Context, chainID ids.ID, requestID uint32) error {
	return nil
}

func (vm *VM) CrossChainAppResponse(ctx context.Context, chainID ids.ID, requestID uint32, response []byte) error {
	return nil
}
