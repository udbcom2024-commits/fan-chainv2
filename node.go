package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"fan-chain/api"
	"fan-chain/config"
	"fan-chain/consensus"
	"fan-chain/core"
	"fan-chain/network"
	"fan-chain/state"
	"fan-chain/storage"
)

type Node struct {
	config    *config.Config
	db        *storage.Database
	chain     *core.Blockchain
	state     *state.StateManager
	consensus *consensus.ConsensusEngine
	p2pServer *network.Server
	apiServer *api.Server

	address      string
	privateKey   []byte
	publicKey    []byte
	pendingTxDir string

	// 验证者激活状态（安全机制：防止未同步节点出块）
	validatorActivated bool
	syncedHeight       uint64 // 记录同步完成时的高度

	// Checkpoint同步状态
	needCheckpointBlock     bool   // 是否需要请求checkpoint高度的完整区块
	checkpointHeight        uint64 // checkpoint的高度
	needSyncCheckpointBlock bool   // 【Ephemeral】是否需要同步真实的checkpoint区块
}

func NewNode(cfg *config.Config) (*Node, error) {
	if err := cfg.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("failed to create directories: %v", err)
	}

	db, err := storage.OpenDatabase(cfg.DBPath())
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %v", err)
	}

	stateManager := state.NewStateManager(db)
	consensusEngine := consensus.NewConsensusEngine(stateManager)
	blockchain := core.NewBlockchain()

	pendingTxDir := filepath.Join(cfg.DataDir, "pending_txs")
	if err := os.MkdirAll(pendingTxDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create pending_txs dir: %v", err)
	}

	node := &Node{
		config:       cfg,
		db:           db,
		chain:        blockchain,
		state:        stateManager,
		consensus:    consensusEngine,
		pendingTxDir: pendingTxDir,
	}

	// 设置验证者变更回调：当质押/解押导致验证者集合变化时，实时更新共识层
	stateManager.SetValidatorCallbacks(
		// onValidatorAdded: 新验证者加入
		func(address string, stakedAmount uint64) {
			validator := &core.Validator{
				Address:       address,
				StakedAmount:  stakedAmount,
				Status:        core.ValActive,
				LastBlockTime: time.Now().Unix(),
				LastHeartbeat: time.Now().Unix(),
			}
			consensusEngine.ValidatorSet().AddValidator(validator)
		},
		// onValidatorRemoved: 验证者退出
		func(address string) {
			consensusEngine.ValidatorSet().RemoveValidator(address)
		},
	)

	return node, nil
}

func (n *Node) InitializeAPI() error {
	n.apiServer = api.NewServer(n.config.APIPort, n.db, n.state, n.chain)

	n.apiServer.SetCallbacks(
		func() *core.Block {
			return n.chain.GetLatestBlock()
		},
		func() int {
			if n.p2pServer != nil {
				return n.p2pServer.PeerCount()
			}
			return 0
		},
		func() string {
			return n.address
		},
		func() string {
			return n.config.NodeName
		},
		func(tx *core.Transaction) error {
			return n.SubmitTransaction(tx)
		},
	)

	go func() {
		if err := n.apiServer.Start(); err != nil {
			log.Fatalf("API server failed: %v", err)
		}
	}()

	return nil
}

func (n *Node) Close() {
	if n.db != nil {
		n.db.Close()
	}
}

func (n *Node) StartCleanupTask() {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := n.db.CleanupOldBlocks(time.Now().Unix()); err != nil {
				log.Printf("Cleanup failed: %v", err)
			}
		}
	}()
}

func (n *Node) isActiveValidator(address string) bool {
	validators := n.consensus.ValidatorSet().GetActiveValidators()
	for _, v := range validators {
		if v.Address == address {
			return true
		}
	}
	return false
}

func (n *Node) getPeerCount() int {
	if n.p2pServer == nil {
		return 0
	}
	return n.p2pServer.PeerCount()
}
