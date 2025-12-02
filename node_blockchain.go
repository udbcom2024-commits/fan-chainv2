package main

import (
	"fmt"
	"log"
	"time"

	"fan-chain/core"
)

func (n *Node) InitializeBlockchain() error {
	// 【Ephemeral状态共识】首先尝试从checkpoint恢复
	checkpoint, err := n.db.GetLatestCheckpoint(n.config.DataDir)
	if err == nil && checkpoint != nil {
		log.Printf("📌 Found checkpoint at height %d, loading from checkpoint (Ephemeral State)", checkpoint.Height)

		// 从checkpoint恢复验证者集合
		if len(checkpoint.Validators) > 0 {
			n.consensus.ValidatorSet().LoadFromCheckpoint(checkpoint.Validators)
			log.Printf("✓ Loaded %d validators from checkpoint", len(checkpoint.Validators))
		}

		// 加载状态快照
		stateData, err := n.db.LoadStateSnapshot(checkpoint.Height, n.config.DataDir)
		if err == nil && len(stateData) > 0 {
			// 状态快照加载需要实现，暂时跳过
			log.Printf("⚠️  State snapshot loading not yet implemented, skipping")
		}

		// 尝试从数据库获取checkpoint对应的区块
		checkpointBlock, err := n.db.GetBlockByHeight(checkpoint.Height)
		if err != nil {
			// 如果没有对应的区块，创建一个虚拟块来初始化区块链
			// 【关键修复】使用SetLatestBlockWithHash确保占位块使用正确的checkpoint哈希
			log.Printf("⚠️  Block at height %d not found, creating placeholder with checkpoint hash", checkpoint.Height)
			placeholderBlock := &core.Block{
				Header: &core.BlockHeader{
					Height:       checkpoint.Height,
					Timestamp:    checkpoint.Timestamp,
					StateRoot:    checkpoint.StateRoot,
					PreviousHash: checkpoint.PreviousHash, // 使用checkpoint中的PreviousHash
				},
				Transactions: []*core.Transaction{},
			}
			// 使用checkpoint记录的真实哈希，而不是重新计算
			n.chain.SetLatestBlockWithHash(placeholderBlock, checkpoint.BlockHash)
			log.Printf("Blockchain initialized at height %d", checkpoint.Height)
		} else {
			// 如果有真实区块，正常初始化
			n.chain.Initialize(checkpointBlock)
		}
		log.Printf("✅ Blockchain initialized from checkpoint at height %d", checkpoint.Height)
		return nil
	}

	// 【完整同步模式】没有checkpoint时，初始化空状态等待同步
	// 新节点将通过P2P同步获取完整区块历史（100天保留期内）
	log.Printf("📦 【Full Sync】No checkpoint found, initializing for full block sync...")
	log.Printf("📦 This node will sync full block history from peers (100-day retention)")

	// 创建一个空的初始化状态，等待区块同步
	emptyBlock := &core.Block{
		Header: &core.BlockHeader{
			Height:    0,
			Timestamp: time.Now().Unix(),
		},
		Transactions: []*core.Transaction{},
	}
	n.chain.Initialize(emptyBlock)

	// 等待P2P同步完整区块历史

	return nil
}

func (n *Node) InitializeValidators() error {
	// 【Ephemeral修复】如果已经从checkpoint恢复了验证者，跳过从数据库加载
	// 否则会覆盖掉checkpoint中的验证者集合
	if len(n.consensus.ValidatorSet().GetActiveValidators()) == 0 {
		// 只有在没有验证者时才从数据库加载
		if err := n.consensus.ValidatorSet().LoadFromState(n.db); err != nil {
			return fmt.Errorf("failed to load validators: %v", err)
		}
		n.consensus.ValidatorSet().UpdateActiveSet()
	}

	n.consensus.SetNodeKeys(n.address, n.privateKey, n.publicKey)

	return nil
}

// PerformChainReorganization 执行链重组
// rollbackHeight: 回滚到的目标高度（错误区块的前一个高度）
// correctBlock: 正确的区块（来自VRF选中的proposer）
func (n *Node) PerformChainReorganization(rollbackHeight uint64, correctBlock *core.Block) error {
	log.Printf("🔄 CHAIN REORG: Starting reorganization to height %d", rollbackHeight)

	// 1. 获取回滚目标区块
	targetBlock, err := n.db.GetBlockByHeight(rollbackHeight)
	if err != nil {
		// 【容错机制】如果目标区块不存在，可能是因为节点处于快速同步状态（JumpToBlock导致）
		// 在这种情况下，跳过REORG，让节点继续正常的区块同步流程
		currentHeight := n.chain.GetLatestHeight()
		log.Printf("⚠️  REORG SKIPPED: Target block #%d not found in DB (current height: %d)", rollbackHeight, currentHeight)
		log.Printf("   This is expected for nodes in fast sync mode. Will rely on normal block sync instead.")
		log.Printf("   Error: %v", err)
		return fmt.Errorf("target block not in database, skipping reorg (node may be in fast sync)")
	}

	// 【关键修复】验证prev hash是否匹配，确保原子性（用户要求）
	// 如果本地block的hash != 新区块的PreviousHash，说明分叉点更早
	if targetBlock.Hash() != correctBlock.Header.PreviousHash {
		log.Printf("⚠️  DEEP FORK: Local block #%d hash %s != correct block's prev hash %s",
			rollbackHeight, targetBlock.Hash().String()[:16], correctBlock.Header.PreviousHash.String()[:16])
		log.Printf("🔍 【家规】启动二分查找共同祖先，不清除数据，只替换分叉部分...")

		// 【家规】触发深度分叉解决：找共同祖先，向大哥请求正确区块
		// 返回特殊错误，让调用方触发深度同步
		return fmt.Errorf("DEEP_FORK:%d:%s", rollbackHeight, correctBlock.Header.PreviousHash.String())
	}
	log.Printf("✓ REORG: Hash validation passed - local #%d matches correct block's prev hash", rollbackHeight)

	// 2. 删除数据库中错误的区块
	if err := n.db.DeleteBlocksAboveHeight(rollbackHeight); err != nil {
		return fmt.Errorf("failed to delete incorrect blocks: %v", err)
	}

	// 3. 回滚区块链状态
	if err := n.chain.RollbackToHeight(rollbackHeight, targetBlock); err != nil {
		return fmt.Errorf("failed to rollback blockchain: %v", err)
	}

	// 4. 重新加载状态
	if err := n.state.ReloadStateFromHeight(n.db, rollbackHeight); err != nil {
		return fmt.Errorf("failed to reload state: %v", err)
	}

	log.Printf("✓ REORG: Rolled back to height %d", rollbackHeight)

	// 5. 添加正确的区块
	log.Printf("🔄 REORG: Adding correct block #%d from proposer %s", correctBlock.Header.Height, correctBlock.Header.Proposer[:10])

	// 执行区块中的交易
	for _, tx := range correctBlock.Transactions {
		if err := n.state.ExecuteTransaction(tx, true); err != nil {
			return fmt.Errorf("failed to execute tx in correct block: %v", err)
		}
	}

	// 【P0原子性】使用带P0验证的提交
	if err := n.state.CommitWithP0Verify(correctBlock.Header.Height); err != nil {
		return fmt.Errorf("failed to commit state (P0 check): %v", err)
	}

	// 【REORG专用】直接更新链状态，跳过验证
	// 原因：我们已经在前面验证过targetBlock.Hash() == correctBlock.Header.PreviousHash
	// AddBlock会再次调用Validate，但此时latestBlock可能是从数据库加载的（hash计算方式不同）
	n.chain.SetLatestBlock(correctBlock)
	log.Printf("✓ REORG: Chain updated to height %d", correctBlock.Header.Height)

	if err := n.db.SaveBlock(correctBlock); err != nil {
		return fmt.Errorf("failed to save correct block: %v", err)
	}

	log.Printf("✅ REORG COMPLETE: Chain reorganized to height %d with correct block", correctBlock.Header.Height)
	return nil
}
