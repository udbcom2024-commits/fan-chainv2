package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"fan-chain/core"
	"fan-chain/crypto"
)

func (n *Node) StartBlockProduction() {
	var lastProposer string
	var waitCount int
	const maxWaitTime = 1 // 5秒failover（1个区块周期 × 5秒）- 符合fan.md P5协议

	// 【架构变更】验证者集合只从Checkpoint加载，不需要定时重载
	// 初始加载会在节点启动时通过LoadLatestCheckpoint完成

	for {
		if !n.isActiveValidator(n.address) {
			time.Sleep(5 * time.Second)
			continue
		}

		// 安全检查：验证者必须先激活才能出块
		if !n.validatorActivated {
			log.Printf("Validator not yet activated, waiting for network confirmation...")
			time.Sleep(5 * time.Second)
			continue
		}

		currentHeight := n.chain.GetLatestHeight()
		if len(n.config.SeedPeers) > 0 && currentHeight == 0 {
			time.Sleep(2 * time.Second)
			continue
		}

		n.chain.WaitForNextBlock()

		latestBlock := n.chain.GetLatestBlock()
		nextHeight := n.chain.GetLatestHeight() + 1

		// 【架构变更】验证者集合只在Checkpoint时更新，不需要定时重载

		// 【关键】VRF选择proposer - 这是防止分叉的核心
		proposer, err := n.consensus.SelectProposer(nextHeight, latestBlock.Hash())
		if err != nil {
			log.Printf("Failed to select proposer: %v", err)
			continue
		}

		// 【强制规则】只有VRF选中的validator才能出块
		if proposer != n.address {
			// 不是自己的轮次，等待
			if proposer != lastProposer {
				// 新的proposer
				log.Printf("Block #%d: VRF selected %s (not me), waiting...", nextHeight, proposer[:10])
				lastProposer = proposer
				waitCount = 1
			} else {
				// 同一个proposer持续多轮
				waitCount++

				if waitCount >= maxWaitTime {
					// 【家长制】Failover前检查：如果有大哥（peer高度更高），不要Failover！
					// 应该等同步完成，而不是自己出块导致分叉
					if n.p2pServer != nil {
						bestPeerHeight := n.p2pServer.GetBestPeerHeight()
						if bestPeerHeight > nextHeight {
							// 有大哥在出块！我落后了，不要Failover，继续等同步
							log.Printf("👑 【家长制】大哥高度 %d > 我的下一个高度 %d，不Failover，等同步！", bestPeerHeight, nextHeight)
							waitCount = 0 // 重置等待计数
							// 主动触发同步请求
							n.p2pServer.RequestSyncFromBestPeer(nextHeight, bestPeerHeight+100)
							time.Sleep(2 * time.Second) // 等待同步
							continue
						}
					}

					// 没有大哥（所有peer都不比我高），可以Failover
					log.Printf("⚠️ Validator %s timeout after %d block (5s), I will take over!", proposer[:10], maxWaitTime)

					// 【关键修复】重置状态并直接出块，不再等待或同步
					waitCount = 0
					lastProposer = ""

					// 直接跳出等待循环，进入出块流程
					// 不需要再检查 proposer != n.address，直接由自己出块
					log.Printf("🔄 Failover: Taking over block production for height %d", nextHeight)
					goto PRODUCE_BLOCK
				}
			}
			time.Sleep(time.Second)
			continue
		}

	PRODUCE_BLOCK:

		// 【家长制-统一检查】无论是VRF选中自己还是Failover，出块前都必须检查是否落后
		// 这是100天修剪机制的要求：从checkpoint恢复后必须先同步区块历史，不能直接出块
		if n.p2pServer != nil {
			bestPeerHeight := n.p2pServer.GetBestPeerHeight()
			if bestPeerHeight > nextHeight {
				// 有大哥在前面！我落后了，必须先同步，不能出块
				log.Printf("👑 【家长制】出块前检查：大哥高度 %d > 我的下一个高度 %d，先同步！", bestPeerHeight, nextHeight)
				n.p2pServer.RequestSyncFromBestPeer(nextHeight, bestPeerHeight+100)
				time.Sleep(2 * time.Second)
				continue
			}
		}

		// 【生存活性第一】基于checkpoint出块，不强制要求历史区块
		// latestBlock已经从checkpoint恢复，包含正确的hash，可以直接出块
		// 历史区块同步是后台任务，不阻塞出块
		if nextHeight > 1 {
			prevBlockInDB, err := n.db.GetBlockByHeight(nextHeight - 1)
			if err != nil || prevBlockInDB == nil {
				// 前一个区块不在数据库中，但我们有checkpoint状态
				// 【生存活性】直接基于内存中的latestBlock出块
				log.Printf("📦 【生存活性】区块 #%d 不在DB中，基于checkpoint状态出块", nextHeight-1)
			} else if prevBlockInDB.Hash() != latestBlock.Hash() {
				// 数据库区块与链状态不一致，以链状态为准
				log.Printf("⚠️ 【链状态优先】DB区块hash不一致，以内存链状态为准")
			}
		}

		// 【关键修复】出块前短暂等待并再次检查高度
		// 确保没有在等待期间收到该高度的区块（防止激活后立即出块导致分叉）
		time.Sleep(500 * time.Millisecond)
		actualHeight := n.chain.GetLatestHeight()
		if actualHeight >= nextHeight {
			log.Printf("⚠ Height changed during wait: expected to produce #%d, but height is now %d",
				nextHeight, actualHeight)
			continue
		}

		// 确认可以出块
		lastProposer = ""
		waitCount = 0
		log.Printf("✓ Block #%d: I am VRF selected proposer, producing block...", nextHeight)

		if err := n.produceBlock(nextHeight, latestBlock); err != nil {
			log.Printf("Failed to produce block: %v", err)
			continue
		}
	}
}

func (n *Node) produceBlock(height uint64, prevBlock *core.Block) error {
	// 计算新区块时间戳（毫秒级）：确保至少比前一区块大出块间隔，避免竞争出块时时间戳冲突
	blockIntervalMs := int64(core.BlockInterval()) * 1000 // 转换为毫秒
	minTimestamp := prevBlock.Header.Timestamp + blockIntervalMs
	currentTimestamp := time.Now().UnixMilli()

	// 使用两者中的较大值，确保时间戳严格递增
	var newTimestamp int64
	if currentTimestamp >= minTimestamp {
		newTimestamp = currentTimestamp
	} else {
		newTimestamp = minTimestamp
	}

	header := &core.BlockHeader{
		Height:       height,
		PreviousHash: prevBlock.Hash(),
		Timestamp:    newTimestamp,
		Proposer:     n.address,
		StateRoot:    n.consensus.CalculateStateRoot(),
	}

	vrfSeed := append(prevBlock.Hash().Bytes(), core.Uint64ToBytes(height)...)
	vrfProof, err := crypto.ComputeVRF(n.privateKey, vrfSeed)
	if err != nil {
		return fmt.Errorf("failed to compute VRF: %v", err)
	}
	header.VRFProof = vrfProof.Proof
	header.VRFOutput = vrfProof.Output

	rawUserTxs := n.loadPendingTransactions()
	userTxs := n.validateAndDeduplicateTransactions(rawUserTxs)

	activeVals := n.consensus.ValidatorSet().GetActiveValidators()
	rewardTxs := n.consensus.CreateRewardTransactions(n.address, activeVals)

	allTxs := append(userTxs, rewardTxs...)

	tempBlock := &core.Block{
		Header:       header,
		Transactions: allTxs,
	}
	header.TxRoot = tempBlock.CalculateTxRoot()

	headerData := header.SignData()
	signature, err := crypto.Sign(n.privateKey, headerData)
	if err != nil {
		return fmt.Errorf("failed to sign block: %v", err)
	}
	header.Signature = signature

	block := &core.Block{
		Header:       header,
		Transactions: allTxs,
	}

	// 动态容量检测：尝试添加Data字段
	if err := n.tryAddBlockData(block, height); err != nil {
		log.Printf("Warning: failed to add block data: %v", err)
		// 不影响出块，继续
	}

	if err := n.chain.ValidateBlock(block); err != nil {
		return fmt.Errorf("block validation failed: %v", err)
	}

	stateSnapshot := n.state.CreateSnapshot()

	// 执行区块中的交易（严格验证，因为是新产生的区块）
	for _, tx := range block.Transactions {
		if err := n.state.ExecuteTransaction(tx, false); err != nil {
			n.state.RestoreSnapshot(stateSnapshot)
			return fmt.Errorf("failed to execute tx: %v", err)
		}
	}

	// 【原子性提交顺序】区块先落盘，状态后提交
	// 1. 先保存区块到数据库（区块落盘）
	if err := n.db.SaveBlock(block); err != nil {
		n.state.RestoreSnapshot(stateSnapshot)
		return fmt.Errorf("failed to save block: %v", err)
	}

	// 2. 提交状态并更新state_height（P0验证+原子性标记）
	// 如果崩溃发生在这里，重启时会检测到block_height > state_height，触发重放
	if err := n.state.CommitWithP0Verify(height); err != nil {
		// 状态提交失败，但区块已保存。重启时会重放此区块
		n.state.RestoreSnapshot(stateSnapshot)
		return fmt.Errorf("failed to commit state (P0 check): %v", err)
	}

	// 3. 更新内存中的链状态
	if err := n.chain.AddBlock(block); err != nil {
		// 内存状态更新失败不影响持久化数据，重启会恢复
		return fmt.Errorf("failed to add block: %v", err)
	}

	if n.p2pServer != nil {
		n.p2pServer.BroadcastBlock(block)
	}

	log.Printf("Block #%d: %s (Rewards: %d)", height, block.Hash().String()[:16], len(rewardTxs))

	// 根据共识配置生成checkpoint
	checkpointInterval := core.GetConsensusConfig().BlockParams.CheckpointInterval
	if height%uint64(checkpointInterval) == 0 {
		if err := n.generateCheckpoint(height, block); err != nil {
			log.Printf("Warning: failed to generate checkpoint at height %d: %v", height, err)
		}
	}

	return nil
}

func (n *Node) loadPendingTransactions() []*core.Transaction {
	txs := make([]*core.Transaction, 0)

	files, err := os.ReadDir(n.pendingTxDir)
	if err != nil {
		return txs
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		filePath := filepath.Join(n.pendingTxDir, file.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var tx core.Transaction
		if err := json.Unmarshal(data, &tx); err != nil {
			continue
		}

		txs = append(txs, &tx)
		os.Remove(filePath)
	}

	return txs
}

// generateCheckpoint 生成检查点（包含总量检查）
func (n *Node) generateCheckpoint(height uint64, block *core.Block) error {
	log.Printf("📌 Generating checkpoint at height %d...", height)

	// 【总量检查机制】验证系统总供应量是否为14亿
	if err := n.verifyTotalSupply(); err != nil {
		log.Printf("❌ Total supply check failed: %v", err)
		log.Printf("🔄 Starting rollback to find last valid block...")

		// 查找最后一个总量正确的区块
		validHeight, err := n.findLastValidBlock()
		if err != nil {
			return fmt.Errorf("failed to find valid block: %v", err)
		}

		// 回退到正确的高度
		if validHeight < height {
			log.Printf("⚠️ Rolling back from height %d to %d", height, validHeight)
			targetBlock, err := n.db.GetBlockByHeight(validHeight)
			if err != nil {
				return fmt.Errorf("failed to get target block: %v", err)
			}

			// 执行回退
			if err := n.chain.RollbackToHeight(validHeight, targetBlock); err != nil {
				return fmt.Errorf("failed to rollback: %v", err)
			}

			// 重新加载状态
			if err := n.state.ReloadStateFromHeight(n.db, validHeight); err != nil {
				return fmt.Errorf("failed to reload state: %v", err)
			}

			// 删除错误的区块
			if err := n.db.DeleteBlocksAboveHeight(validHeight); err != nil {
				log.Printf("Warning: failed to delete invalid blocks: %v", err)
			}

			// 使用回退后的区块生成checkpoint
			height = validHeight
			block = targetBlock
			log.Printf("✅ Rolled back to height %d with correct total supply", height)
		}
	} else {
		log.Printf("✅ Total supply check passed: 1400000000 FAN")
	}

	// 计算StateRoot
	stateRoot, err := n.state.CalculateStateRoot()
	if err != nil {
		return fmt.Errorf("failed to calculate state root: %v", err)
	}

	// 创建checkpoint（包含PreviousHash用于链接）
	checkpoint := core.NewCheckpoint(
		height,
		block.Hash(),
		block.Header.PreviousHash,  // 添加前一个区块哈希
		stateRoot,
		block.Header.Timestamp,
		n.address,
	)

	// 【竞争性激活】从所有质押账户中选择前N名作为活跃验证者
	// 1. 获取所有满足最低质押要求的账户
	consensusConfig := core.GetConsensusConfig()
	minStake := consensusConfig.EconomicParams.ValidatorStakeRequired
	maxValidators := consensusConfig.ValidatorParams.MaxValidators

	// 【重要】使用合并后的账户列表（数据库+缓存），确保不遗漏任何账户
	allAccounts, err := n.state.GetAllAccountsMerged()
	if err != nil {
		return fmt.Errorf("failed to get all accounts: %v", err)
	}

	// 2. 筛选出所有质押账户
	type candidateValidator struct {
		address       string
		stakedBalance uint64
		vrfPublicKey  []byte
	}
	candidates := make([]candidateValidator, 0)

	for _, acc := range allAccounts {
		if acc.StakedBalance >= minStake {
			// 【注意】Account结构中没有VRFPublicKey字段
			// VRF公钥在实际使用中从节点公钥获取，这里设为空
			candidates = append(candidates, candidateValidator{
				address:       acc.Address,
				stakedBalance: acc.StakedBalance,
				vrfPublicKey:  []byte{}, // 设为空字节数组
			})
		}
	}

	// 3. 按质押量排序（降序）
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].stakedBalance > candidates[j].stakedBalance
	})

	// 4. 取前N名作为活跃验证者
	activeCount := len(candidates)
	if activeCount > maxValidators {
		activeCount = maxValidators
		log.Printf("📊 Checkpoint: %d candidates, selecting top %d validators", len(candidates), maxValidators)
	} else {
		log.Printf("📊 Checkpoint: %d active validators", activeCount)
	}

	// 5. 创建验证者快照
	for i := 0; i < activeCount; i++ {
		candidate := candidates[i]

		// 提取VRF公钥的前32字节作为精简版本
		vrfKey := candidate.vrfPublicKey
		if len(vrfKey) > 32 {
			vrfKey = vrfKey[:32]
		}

		snapshot := core.ValidatorSnapshot{
			Address:   candidate.address,
			Stake:     candidate.stakedBalance,
			VRFPubKey: vrfKey,
		}
		checkpoint.Validators = append(checkpoint.Validators, snapshot)

		log.Printf("  ✓ Validator[%d]: %s (stake: %d FAN)",
			i+1, candidate.address[:10], candidate.stakedBalance/1000000)
	}

	// 6. 如果有候选者未能激活，记录日志
	if len(candidates) > maxValidators {
		log.Printf("⚠️  %d candidates did not make it into active set:", len(candidates)-maxValidators)
		for i := maxValidators; i < len(candidates); i++ {
			log.Printf("    [%d] %s (stake: %d FAN)",
				i+1, candidates[i].address[:10], candidates[i].stakedBalance/1000000)
		}
	}

	// 签名checkpoint
	if err := checkpoint.Sign(n.privateKey); err != nil {
		return fmt.Errorf("failed to sign checkpoint: %v", err)
	}

	// 保存checkpoint文件
	if err := n.db.SaveCheckpoint(checkpoint, n.config.DataDir); err != nil {
		return fmt.Errorf("failed to save checkpoint: %v", err)
	}

	// 创建状态快照
	snapshot, err := n.state.CreateCheckpointSnapshot(height)
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %v", err)
	}

	// 保存状态快照（使用单点管理）
	compressedData, err := snapshot.Compress()
	if err != nil {
		return fmt.Errorf("failed to compress snapshot: %v", err)
	}
	if err := n.db.SaveStateSnapshot(height, compressedData, n.config.DataDir); err != nil {
		return fmt.Errorf("failed to save snapshot: %v", err)
	}

	// 单点checkpoint设计：不需要清理，SaveCheckpoint已经强制删除旧文件

	log.Printf("✅ Checkpoint created at height %d, StateRoot: %s", height, stateRoot.String()[:16])

	// 广播checkpoint和状态快照给所有peers（让History等节点直接接收）
	if n.p2pServer != nil {
		n.p2pServer.BroadcastCheckpoint(checkpoint, compressedData)
	}

	return nil
}

// tryAddBlockData 尝试向区块添加Data字段（机场链接等）
func (n *Node) tryAddBlockData(block *core.Block, height uint64) error {
	// 1. 读取pending data
	pendingDataPath := filepath.Join(n.config.DataDir, "pending_data", "pending_data.json")
	data, err := os.ReadFile(pendingDataPath)
	if err != nil {
		if os.IsNotExist(err) {
			// 没有待发布数据，正常情况
			return nil
		}
		return fmt.Errorf("failed to read pending data: %v", err)
	}

	if len(data) == 0 {
		// 空文件，跳过
		return nil
	}

	// 2. 计算当前区块大小
	currentSize, err := n.calculateBlockSize(block)
	if err != nil {
		return fmt.Errorf("failed to calculate block size: %v", err)
	}

	// 3. 获取共识参数
	consensusConfig := core.GetConsensusConfig()
	maxBlockSize := consensusConfig.BlockParams.MaxBlockSize
	thresholdPercent := consensusConfig.BlockParams.BlockDataThresholdPercent

	// 4. 计算阈值
	threshold := uint64(maxBlockSize) * uint64(thresholdPercent) / 100

	// 5. 判断是否达到阈值
	if currentSize >= threshold {
		log.Printf("Block size %d >= threshold %d (%d%%), skipping Data field",
			currentSize, threshold, thresholdPercent)
		return nil
	}

	// 6. 加密数据
	encrypted, err := crypto.EncryptData(data, height)
	if err != nil {
		return fmt.Errorf("failed to encrypt data: %v", err)
	}

	// 7. 检查加密后数据是否会超过限制
	newSize := currentSize + uint64(len(encrypted))
	if newSize > uint64(maxBlockSize) {
		log.Printf("Block size with data %d > max %d, skipping Data field",
			newSize, maxBlockSize)
		return nil
	}

	// 8. 添加到区块
	block.Data = encrypted
	log.Printf("✓ Added %d bytes encrypted data to block #%d (total: %d bytes)",
		len(encrypted), height, newSize)

	return nil
}

// calculateBlockSize 计算区块序列化后的大小
func (n *Node) calculateBlockSize(block *core.Block) (uint64, error) {
	// 序列化区块
	blockJSON, err := json.Marshal(block)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal block: %v", err)
	}

	return uint64(len(blockJSON)), nil
}

// verifyTotalSupply 验证系统总供应量是否为14亿FAN
func (n *Node) verifyTotalSupply() error {
	const TOTAL_SUPPLY = uint64(1400000000000000) // 14亿 FAN

	// 【P0双重验证】使用StateManager的双重验证方法，它会包含缓存中的账户
	totalSupply, isCorrect, err := n.state.VerifyTotalSupplyDual()
	if err != nil {
		return fmt.Errorf("failed to verify total supply: %v", err)
	}

	// 验证总量是否正确
	if !isCorrect {
		return fmt.Errorf("total supply mismatch: expected %d, got %d (diff: %d)",
			TOTAL_SUPPLY, totalSupply, int64(TOTAL_SUPPLY) - int64(totalSupply))
	}

	return nil
}

// findLastValidBlock 查找最后一个总量正确的区块
func (n *Node) findLastValidBlock() (uint64, error) {
	currentHeight := n.chain.GetLatestHeight()

	// 从当前高度向前回溯，最多回溯100个区块
	for height := currentHeight; height > 0 && currentHeight-height < 100; height-- {
		// 恢复到该高度的状态
		if err := n.state.ReloadStateFromHeight(n.db, height); err != nil {
			continue
		}

		// 检查该高度的总量
		if err := n.verifyTotalSupply(); err == nil {
			log.Printf("✅ Found valid block with correct total supply at height %d", height)
			return height, nil
		}
	}

	// 如果没找到，返回创世块高度
	log.Printf("⚠️ Could not find valid block, returning genesis height")
	return 1, nil
}
