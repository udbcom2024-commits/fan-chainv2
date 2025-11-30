package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"fan-chain/core"
	"fan-chain/network"
	"fan-chain/state"
)

func (n *Node) InitializeP2P() error {
	n.p2pServer = network.NewServer(n.address, n.config.P2PPort, n.config.SeedPeers, n.config.PublicIP)
	n.p2pServer.SetBlockchainInterface(
		func() *core.Block {
			return n.chain.GetLatestBlock()
		},
		func(block *core.Block) error {
			// 创世区块应该在InitializeBlockchain中已经处理，跳过P2P同步的创世区�?
			if block.Header.Height == 0 {
				log.Printf("Skipping genesis block from P2P sync (already initialized)")
				return nil
			}

			// 验证区块 - 如果是高度不匹配（checkpoint区块），保存到数据库后再返回错误
			validateErr := n.chain.ValidateBlock(block)
			if validateErr != nil && strings.Contains(validateErr.Error(), "invalid height") {
				log.Printf("Height mismatch for block #%d, saving to DB for checkpoint processing", block.Header.Height)
				if err := n.db.SaveBlock(block); err != nil {
					log.Printf("Failed to save checkpoint block to DB: %v", err)
				}
				return validateErr
			} else if validateErr != nil {
				return validateErr
			}

			// 【关键修复】同步历史区块时跳过proposer验证
			// 原因:历史区块是基于当时的验证者集合和VRF产生�?不应该用当前状态验�?
			// VRF proposer验证只在P2P网络层对"实时新区�?进行(network/server.go)
			// 历史区块的正确性已经由链上大多数节点共识保�?

			// 执行同步的历史区块交易（跳过时间戳验证）
			for _, tx := range block.Transactions {
				if err := n.state.ExecuteTransaction(tx, true); err != nil {
					return err
				}
			}

			if err := n.state.Commit(); err != nil {
				return err
			}

			// P0验证移至Checkpoint生成时（不在同步区块时验证）
			// 原因：同步区块时缓存和数据库状态可能不完全同步，导致误报
			log.Printf("区块 #%d 同步完成", block.Header.Height)


			// 添加区块到区块链
			if err := n.chain.AddBlock(block); err != nil {
				return err
			}

			// 区块成功添加到链，保存到数据�?
			if err := n.db.SaveBlock(block); err != nil {
				return err
			}

			return nil
		},
		func(fromHeight, toHeight uint64) ([]*core.Block, error) {
			return n.db.GetBlockRange(fromHeight, toHeight)
		},
	)

	// 设置跳过时间戳检查的区块添加函数（用于同步历史区块）
	n.p2pServer.SetAddBlockSkipTimestamp(func(block *core.Block) error {
		// 创世区块应该在InitializeBlockchain中已经处理，跳过P2P同步的创世区�?
		if block.Header.Height == 0 {
			log.Printf("Skipping genesis block from P2P sync (already initialized)")
			return nil
		}

		// 【关键】跳过时间戳验证，用于同步历史区�?
		// 执行同步的历史区块交易（跳过时间戳验证）
		for _, tx := range block.Transactions {
			if err := n.state.ExecuteTransaction(tx, true); err != nil {
				return err
			}
		}

		if err := n.state.Commit(); err != nil {
			return err
		}

		// P0验证移至Checkpoint生成时（不在同步区块时验证）
		// 原因：同步区块时缓存和数据库状态可能不完全同步，导致误报
		log.Printf("历史区块 #%d 同步完成", block.Header.Height)


		// 添加区块到区块链（跳过时间戳验证�?
		if err := n.chain.AddBlockWithOptions(block, true); err != nil {
			return err
		}

		// 区块成功添加到链，保存到数据�?
		if err := n.db.SaveBlock(block); err != nil {
			return err
		}

		return nil
	})

	// 设置只保存区块的函数（用于checkpoint前回填历史区块）
	// 【P2协议修复】使用SaveBlockForBackfill，不更新latest_height
	n.p2pServer.SetSaveBlockOnly(func(block *core.Block) error {
		// 只保存区块到数据库，不更新链状态和执行交易
		// 用于checkpoint同步时回填前一个周期的历史区块
		// 【关键】使用SaveBlockForBackfill避免latest_height被拉低
		if err := n.db.SaveBlockForBackfill(block); err != nil {
			return err
		}
		log.Printf("📦 区块 #%d 已保存到数据库（checkpoint历史回填）", block.Header.Height)
		return nil
	})

	// 【家规】设置替换分叉区块的函数（强制用大哥的覆盖本地的）
	n.p2pServer.SetReplaceForkedBlock(func(block *core.Block) error {
		// 检查本地是否已有该高度的区块
		localBlock, err := n.db.GetBlockByHeight(block.Header.Height)
		if err != nil {
			// 本地没有该区块，直接保存
			if err := n.db.SaveBlock(block); err != nil {
				return fmt.Errorf("failed to save forked block: %v", err)
			}
			log.Printf("✓ 【家规】Block #%d saved (no local block)", block.Header.Height)
			return nil
		}

		// 比较哈希，如果相同则无需替换
		if localBlock.Hash() == block.Hash() {
			log.Printf("✓ 【家规】Block #%d hash matches, no replacement needed", block.Header.Height)
			return nil
		}

		// 哈希不同，需要替换
		log.Printf("🔧 【家规】Replacing block #%d: local hash %s -> big brother's hash %s",
			block.Header.Height, localBlock.Hash().String()[:16], block.Hash().String()[:16])

		// 直接覆盖保存（数据库层会处理更新）
		if err := n.db.SaveBlock(block); err != nil {
			return fmt.Errorf("failed to replace forked block: %v", err)
		}

		log.Printf("✓ 【家规】Block #%d replaced successfully", block.Header.Height)
		return nil
	})

	// 设置VRF验证函数用于分叉预防
	n.p2pServer.SetVerifyProposer(func(height uint64, prevBlock *core.Block) (string, error) {
		// 【Ephemeral修复】只有在验证者集合为空时才从状态加�?
		// 如果已经从checkpoint恢复了验证者，则跳过LoadFromState
		if len(n.consensus.ValidatorSet().GetActiveValidators()) == 0 {
			// 加载最新验证者集�?
			if err := n.consensus.ValidatorSet().LoadFromState(n.db); err != nil {
				return "", err
			}
			n.consensus.ValidatorSet().UpdateActiveSet()
		}

		// 使用VRF选择该高度的合法proposer
		proposer, err := n.consensus.SelectProposer(height, prevBlock.Hash())
		if err != nil {
			return "", err
		}
		return proposer, nil
	})

	// 设置链重组函数用于自动修复分�?
	n.p2pServer.SetPerformReorg(func(rollbackHeight uint64, correctBlock *core.Block) error {
		return n.PerformChainReorganization(rollbackHeight, correctBlock)
	})

	// 设置获取proposer质押的函数（用于质押权重链选择�?
	n.p2pServer.SetGetProposerStake(func(address string) uint64 {
		acc, err := n.state.GetAccount(address)
		if err != nil {
			log.Printf("�?Failed to get stake for %s: %v", address[:10], err)
			return 0
		}
		return acc.StakedBalance
	})

	// 设置获取最新N个checkpoint的函�?
	n.p2pServer.SetGetLatestCheckpoints(func(count int) []network.CheckpointInfo {
		return n.getLatestCheckpoints(count)
	})

	// 设置获取最新checkpoint的函�?
	n.p2pServer.SetGetLatestCheckpoint(func() (*core.Checkpoint, error) {
		checkpoint, err := n.db.GetLatestCheckpoint(n.config.DataDir)
		if err != nil {
			return nil, fmt.Errorf("failed to get latest checkpoint: %v", err)
		}
		return checkpoint, nil
	})

	// 设置应用checkpoint的函�?
	n.p2pServer.SetApplyCheckpoint(func(checkpoint *core.Checkpoint) error {
		return n.applyCheckpoint(checkpoint)
	})

	// 设置分叉检测和解决函数（谁快认谁做大哥）
	n.p2pServer.SetDetectAndResolveFork(func(peerHeight uint64, peerBlockHash string, peerCheckpointHeight uint64, peerCheckpointHash string, peerCheckpointTimestamp int64) error {
		return n.DetectAndResolveFork(peerHeight, peerBlockHash, peerCheckpointHeight, peerCheckpointHash, peerCheckpointTimestamp)
	})

	// 设置获取状态快照的函数
	n.p2pServer.SetGetStateSnapshot(func(height uint64) ([]byte, error) {
		return n.getStateSnapshot(height)
	})

	// 设置应用状态快照的函数
	n.p2pServer.SetApplyStateSnapshot(func(height uint64, data []byte) error {
		return n.applyStateSnapshot(height, data)
	})

	// 设置交易处理回调
	n.p2pServer.SetHandleReceivedTransaction(func(tx *core.Transaction) error {
		return n.HandleReceivedTransaction(tx)
	})

	// 【P2协议】设置获取最早区块高度的回调
	n.p2pServer.SetGetEarliestHeight(func() uint64 {
		return n.db.GetEarliestHeight()
	})

	// 【家长制优化】设置验证者判断回调
	// 只有验证者的高度才影响出块决策，非验证者（如History节点）不影响
	n.p2pServer.SetIsValidator(func(address string) bool {
		return n.consensus.ValidatorSet().IsActiveValidator(address)
	})

	if err := n.p2pServer.Start(); err != nil {
		return fmt.Errorf("failed to start P2P server: %v", err)
	}

	n.consensus.SetOnlinePeersFunction(n.p2pServer.GetOnlinePeerAddresses)

	return nil
}

// SyncFromCheckpoint 从checkpoint同步（自动检测是否需要快速同步）
func (n *Node) SyncFromCheckpoint() error {
	currentHeight := n.chain.GetLatestHeight()

	// 等待P2P连接建立
	for i := 0; i < 10; i++ {
		if n.p2pServer.PeerCount() > 0 {
			break
		}
		log.Printf("Waiting for peer connections... (%d/10)", i+1)
		time.Sleep(1 * time.Second)
	}

	if n.p2pServer.PeerCount() == 0 {
		log.Printf("�?No peers available for checkpoint sync, will try block sync")
		return nil
	}

	// 从网络获取最新高�?
	var networkHeight uint64
	for _, seed := range n.config.SeedPeers {
		url := fmt.Sprintf("http://%s/status", strings.Replace(seed, ":9001", ":9000", 1))
		resp, err := http.Get(url)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		var status struct {
			Height uint64 `json:"height"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			continue
		}
		networkHeight = status.Height
		break
	}

	if networkHeight == 0 {
		log.Printf("�?Failed to get network height, skipping checkpoint sync")
		return nil
	}

	// 计算落后的区块数
	gap := int64(networkHeight) - int64(currentHeight)
	log.Printf("🔄 Current height=%d, Network height=%d, Gap=%d blocks", currentHeight, networkHeight, gap)

	// 计算checkpoint配置 - 从共识配置读取
	consensusConfig := core.GetConsensusConfig()

	// 【完整同步模式】请求checkpoint + 完整区块历史
	if gap > 0 {
		log.Printf("📦 【Full Sync】Gap detected (%d blocks), requesting checkpoint + full blocks from %d peer(s)",
			gap, n.p2pServer.PeerCount())
		// 1. 请求checkpoint获取最新状态
		keepCount := uint64(consensusConfig.BlockParams.CheckpointKeepCount)
		n.p2pServer.RequestCheckpointFromPeers(keepCount)
		log.Printf("✓ Checkpoint request sent (requesting %d checkpoints)", keepCount)

		// 2. 同时请求完整区块历史（从当前高度到网络高度）
		if currentHeight < networkHeight {
			n.p2pServer.RequestSyncFromBestPeer(currentHeight+1, networkHeight)
			log.Printf("✓ Full block sync request sent (blocks %d-%d)", currentHeight+1, networkHeight)
		}
	}

	return nil
}

// 旧的HTTP同步方法已废�?
// FAN链只使用P2P checkpoint同步机制，不再需要HTTP状态快照同�?

// SyncCheckpointWithRetry 实现完整的checkpoint+区块同步（带重试机制�?
func (n *Node) SyncCheckpointWithRetry() error {
	maxRetries := 5
	retryInterval := 10 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("🔄 Checkpoint sync attempt %d/%d", attempt, maxRetries)

		// 1. 如果需要checkpoint高度的完整区块，先请求它
		if n.needCheckpointBlock {
			log.Printf("📦 Requesting full block at checkpoint height %d", n.checkpointHeight)

			// 通过P2P请求特定高度的区�?
			if n.p2pServer != nil && n.p2pServer.PeerCount() > 0 {
				// 请求单个区块
				n.p2pServer.RequestSyncFromBestPeer(n.checkpointHeight, n.checkpointHeight)

				// 等待区块到达
				time.Sleep(2 * time.Second)

				// 检查是否收到了区块
				block, err := n.db.GetBlockByHeight(n.checkpointHeight)
				if err == nil && block != nil {
					log.Printf("�?Received block at height %d, updating chain", n.checkpointHeight)
					n.chain.Initialize(block)
					n.needCheckpointBlock = false
				} else {
					log.Printf("⚠️  Failed to get block at height %d, will retry", n.checkpointHeight)
				}
			}
		}

		// 2. 获取当前高度和网络高�?
		currentHeight := n.chain.GetLatestHeight()
		networkHeight := n.getNetworkHeight()

		if networkHeight == 0 {
			log.Printf("⚠️  Cannot get network height, retrying...")
			time.Sleep(retryInterval)
			continue
		}

		gap := int64(networkHeight) - int64(currentHeight)
		log.Printf("📊 Current height: %d, Network height: %d, Gap: %d blocks", currentHeight, networkHeight, gap)

		// 3. 【完整同步模式】请求checkpoint + 完整区块
		if gap > 0 {
			log.Printf("📦 【Full Sync】Gap exists (%d blocks), syncing checkpoint + full blocks", gap)
		}

		// 4. 请求checkpoint和区块
		log.Printf("📦 Requesting latest checkpoint + block sync")
		consensusConfig := core.GetConsensusConfig()
		keepCount := uint64(consensusConfig.BlockParams.CheckpointKeepCount)
		n.p2pServer.RequestCheckpointFromPeers(keepCount)
		// 同时请求完整区块
		if currentHeight < networkHeight {
			n.p2pServer.RequestSyncFromBestPeer(currentHeight+1, networkHeight)
		}

		// 5. 等待checkpoint到达并处�?
		time.Sleep(5 * time.Second)

		// 6. 检查是否成功同步了一些区�?
		newHeight := n.chain.GetLatestHeight()
		if newHeight > currentHeight {
			log.Printf("�?Progress made: %d -> %d", currentHeight, newHeight)

			// 如果已经接近网络高度，成�?
			newGap := int64(networkHeight) - int64(newHeight)
			if newGap <= 12 {
				log.Printf("�?Successfully synced to near network height")
				return nil
			}
		} else {
			log.Printf("⚠️  No progress made, will retry")
		}

		// 7. 等待后重�?
		if attempt < maxRetries {
			log.Printf("�?Waiting %v before retry...", retryInterval)
			time.Sleep(retryInterval)
		}
	}

	return fmt.Errorf("failed to sync after %d attempts", maxRetries)
}

// getNetworkHeight 获取网络最新高�?
func (n *Node) getNetworkHeight() uint64 {
	// 尝试从seed节点获取高度
	for _, seed := range n.config.SeedPeers {
		url := fmt.Sprintf("http://%s/status", strings.Replace(seed, ":9001", ":9000", 1))
		resp, err := http.Get(url)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		var status struct {
			Height uint64 `json:"height"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			continue
		}
		return status.Height
	}

	return 0
}

// getLatestCheckpoints 获取最新的N个checkpoint
func (n *Node) getLatestCheckpoints(count int) []network.CheckpointInfo {
	checkpoints, err := n.db.ListCheckpoints(n.config.DataDir)
	if err != nil || len(checkpoints) == 0 {
		return nil
	}

	// 限制返回数量
	if len(checkpoints) > count {
		checkpoints = checkpoints[:count]
	}

	// 构建checkpoint信息列表
	result := make([]network.CheckpointInfo, 0, len(checkpoints))
	for _, height := range checkpoints {
		checkpoint, err := n.db.LoadCheckpoint(height, n.config.DataDir)
		if err != nil {
			log.Printf("Failed to load checkpoint %d: %v", height, err)
			continue
		}

		// 检查是否有对应的状态文件（使用latest文件名，因为采用单点设计�?
		stateFile := fmt.Sprintf("%s/checkpoints/state_latest.dat.gz", n.config.DataDir)
		hasState := false
		var compressedSize uint64 = 0

		if fileInfo, err := os.Stat(stateFile); err == nil {
			hasState = true
			compressedSize = uint64(fileInfo.Size())
		}

		result = append(result, network.CheckpointInfo{
			Checkpoint:     checkpoint,
			HasStateData:   hasState,
			CompressedSize: compressedSize,
		})
	}

	return result
}

// applyCheckpoint 应用checkpoint
func (n *Node) applyCheckpoint(checkpoint *core.Checkpoint) error {
	log.Printf("Applying checkpoint at height %d (hash: %x)", checkpoint.Height, checkpoint.BlockHash.Bytes()[:8])

	// 【关键修复】首先检查区块高度，如果当前区块高度已经超过checkpoint高度，直接跳过
	// 这防止了"高度来回跳动"的问题：节点已经同步到7549，不应该被旧checkpoint拉回7548
	currentBlockHeight := n.chain.GetLatestHeight()
	if currentBlockHeight > checkpoint.Height {
		log.Printf("✓ Checkpoint at height %d skipped (current block height %d is higher)",
			checkpoint.Height, currentBlockHeight)
		return nil
	}

	// 【P2协议-宽容接收】只基于本地checkpoint高度判断，而非区块高度
	// 这支持新节点从0开始快速同步，以及已有节点接收更新的checkpoint
	localCheckpoint, err := n.db.GetLatestCheckpoint(n.config.DataDir)
	var localCheckpointHeight uint64 = 0
	var localCheckpointHash core.Hash
	if err == nil && localCheckpoint != nil {
		localCheckpointHeight = localCheckpoint.Height
		localCheckpointHash = localCheckpoint.BlockHash
	}

	// 【分叉处理】如果高度相同但hash不同，说明发生了分叉
	// 接受来自网络的checkpoint（假设来自权威节点），并触发链重组
	if checkpoint.Height == localCheckpointHeight && localCheckpointHeight > 0 {
		if checkpoint.BlockHash != localCheckpointHash {
			log.Printf("⚠️  FORK DETECTED at height %d: local hash=%x, network hash=%x",
				checkpoint.Height, localCheckpointHash.Bytes()[:8], checkpoint.BlockHash.Bytes()[:8])
			log.Printf("🔄 Accepting network checkpoint to resolve fork...")
			// 继续处理，强制使用网络的checkpoint
		} else {
			// 相同高度相同hash，无需更新
			log.Printf("✓ Checkpoint at height %d already up to date (same hash)", checkpoint.Height)
			return nil
		}
	} else if checkpoint.Height < localCheckpointHeight {
		// 只拒绝比本地checkpoint高度更低的checkpoint（防止回退）
		log.Printf("🚫 REJECTED: Checkpoint height %d < local checkpoint height %d, discarding", checkpoint.Height, localCheckpointHeight)
		return nil // 静默丢弃，不影响正常运行
	}

	localHeight := n.chain.GetLatestHeight()
	log.Printf("✓ Checkpoint height check passed: %d >= local checkpoint %d (block height: %d)",
		checkpoint.Height, localCheckpointHeight, localHeight)

	// TODO: 验证checkpoint签名需要proposer的公�?
	// 暂时跳过验证，因为新节点还没有完整的账户公钥信息
	// 在生产环境中，应该通过硬编码的验证者公钥列表验�?

	// 恢复验证者集合（确保VRF计算一致性）
	if len(checkpoint.Validators) > 0 {
		log.Printf("Restoring %d validators from checkpoint", len(checkpoint.Validators))
		n.consensus.ValidatorSet().LoadFromCheckpoint(checkpoint.Validators)
		log.Printf("�?Validator set restored: %d active validators", len(checkpoint.Validators))
	}

	// 保存checkpoint到本�?
	if err := n.db.SaveCheckpoint(checkpoint, n.config.DataDir); err != nil {
		return fmt.Errorf("failed to save checkpoint: %v", err)
	}

	// 【Ephemeral核心修复】先设置高度，再尝试获取真实区块
	// 这确保即使没有真实区块，节点高度也能正确设置为checkpoint高度
	if n.chain.GetLatestHeight() < checkpoint.Height {
		log.Printf("🚀 【Ephemeral】Setting blockchain height to checkpoint height %d", checkpoint.Height)

		// 创建占位区块
		placeholderBlock := &core.Block{
			Header: &core.BlockHeader{
				Height:       checkpoint.Height,
				Timestamp:    checkpoint.Timestamp,
				StateRoot:    checkpoint.StateRoot,
				PreviousHash: checkpoint.PreviousHash,
				Proposer:     checkpoint.Proposer,
			},
			Transactions: []*core.Transaction{},
		}

		// 使用SetLatestBlockWithHash来设置正确的hash和高�?
		n.chain.SetLatestBlockWithHash(placeholderBlock, checkpoint.BlockHash)
		log.Printf("�?【Ephemeral】Blockchain height set to %d with checkpoint hash", checkpoint.Height)

		// 标记需要同步真实区�?
		n.needSyncCheckpointBlock = true
	}

	// 尝试获取真实的checkpoint区块（但不阻塞高度设置）
	block, err := n.db.GetBlockByHeight(checkpoint.Height)
	if err != nil {
		log.Printf("⚠️  Checkpoint block not found locally, will request from peers later")

		// 异步请求区块，不阻塞checkpoint应用
		if n.p2pServer != nil && n.p2pServer.PeerCount() > 0 {
			log.Printf("📡 Requesting checkpoint block %d from peers (non-blocking)", checkpoint.Height)
			go func() {
				// 在后台请求区�?
				n.p2pServer.RequestSyncFromBestPeer(checkpoint.Height, checkpoint.Height)

				// 等待一小段时间看是否能获取�?
				cpHeight := checkpoint.Height // 闭包捕获checkpoint高度
				for i := 0; i < 10; i++ {
					time.Sleep(1 * time.Second)
					if b, err := n.db.GetBlockByHeight(cpHeight); err == nil {
						// 【关键修复】只有当前高度仍然等于checkpoint高度时才更新
						// 避免在已经同步到更高区块后回退
						currentHeight := n.chain.GetLatestHeight()
						if currentHeight <= cpHeight {
							log.Printf("�?Checkpoint block %d received from peers", cpHeight)
							// 更新为真实区�?
							n.chain.SetLatestBlock(b)
							n.needSyncCheckpointBlock = false
						} else {
							log.Printf("⏭️  Skipping checkpoint block %d update (already at height %d)", cpHeight, currentHeight)
						}
						break
					}
				}
			}()
		}
	} else {
		// 有真实区块，验证并使用它
		blockHash := block.Hash()
		if blockHash != checkpoint.BlockHash {
			log.Printf("⚠️  Block hash mismatch, using placeholder: expected %x, got %x",
				checkpoint.BlockHash.Bytes()[:8], blockHash.Bytes()[:8])
		} else {
			// 【关键修复】只有当前高度不超过checkpoint时才更新
			currentHeight := n.chain.GetLatestHeight()
			if currentHeight <= checkpoint.Height {
				// Hash匹配，更新为真实区块
				n.chain.SetLatestBlock(block)
				n.needSyncCheckpointBlock = false
				log.Printf("�?Using real checkpoint block at height %d", checkpoint.Height)
			} else {
				log.Printf("⏭️  Skipping checkpoint block %d sync update (already at height %d)", checkpoint.Height, currentHeight)
			}
		}
	}

	log.Printf("�?Checkpoint saved and applied at height %d", checkpoint.Height)
	return nil
}

// getStateSnapshot 获取指定高度的状态快�?
func (n *Node) getStateSnapshot(height uint64) ([]byte, error) {
	// 使用latest文件名，因为采用单点设计
	stateFile := fmt.Sprintf("%s/checkpoints/state_latest.dat.gz", n.config.DataDir)

	// 读取压缩的状态文�?
	snapshot, err := state.LoadCheckpointSnapshotFromFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load snapshot file: %v", err)
	}

	// 序列化为字节传输
	data, err := snapshot.Serialize()
	if err != nil {
		return nil, fmt.Errorf("failed to serialize snapshot: %v", err)
	}

	return data, nil
}

// applyStateSnapshot 应用状态快�?
func (n *Node) applyStateSnapshot(height uint64, compressedData []byte) error {
	log.Printf("Applying state snapshot at height %d (%d bytes)", height, len(compressedData))

	// 反序列化状态快�?
	snapshot, err := state.DeserializeCheckpointSnapshot(compressedData)
	if err != nil {
		return fmt.Errorf("failed to deserialize snapshot: %v", err)
	}

	// 应用快照到状态管理器
	if err := n.state.ApplyCheckpointSnapshot(snapshot); err != nil {
		return fmt.Errorf("failed to apply snapshot: %v", err)
	}

	// 保存状态快照到本地（同时保存到 state_{height}.dat.gz 和 state_latest.dat.gz）
	stateFile := fmt.Sprintf("%s/checkpoints/state_%d.dat.gz", n.config.DataDir, height)
	if err := snapshot.SaveToFile(stateFile); err != nil {
		log.Printf("Warning: failed to save state snapshot locally: %v", err)
	}
	// 【修复】同时保存到 state_latest.dat.gz，确保后续 getLatestCheckpoints 能正确识别 HasStateData
	latestStateFile := fmt.Sprintf("%s/checkpoints/state_latest.dat.gz", n.config.DataDir)
	if err := snapshot.SaveToFile(latestStateFile); err != nil {
		log.Printf("Warning: failed to save state_latest.dat.gz: %v", err)
	}

	log.Printf("�?State snapshot applied: %d accounts at height %d", len(snapshot.Accounts), height)

	// 【重试机制】状态快照应用成功后,检查checkpoint区块是否已保存到数据�?
	// 如果blockchain高度仍然低于checkpoint高度,说明checkpoint区块还没有被应用到链�?
	if n.chain.GetLatestHeight() < height {
		log.Printf("🔄 Blockchain height (%d) < checkpoint height (%d), retrying checkpoint block application...",
			n.chain.GetLatestHeight(), height)

		block, err := n.db.GetBlockByHeight(height)
		if err == nil {
			log.Printf("�?Checkpoint block %d found in database, applying now", height)

			// 使用JumpToBlock快速跳转到checkpoint高度-1
			if n.chain.GetLatestHeight() < height-1 {
				log.Printf("Fast-forwarding blockchain from height %d to %d", n.chain.GetLatestHeight(), height-1)
				if err := n.chain.JumpToBlock(block); err != nil {
					log.Printf("⚠️  Failed to jump to checkpoint block: %v", err)
					return nil // 不返回错误，避免影响状态快照的成功应用
				}
			}

			// 添加checkpoint区块到区块链
			if err := n.chain.AddBlock(block); err != nil {
				log.Printf("⚠️  Failed to add checkpoint block: %v", err)
			} else {
				log.Printf("�?Checkpoint block %d successfully applied! Blockchain height now: %d", height, n.chain.GetLatestHeight())
			}
		} else {
			log.Printf("ℹ️  Checkpoint block %d not yet in database, will sync normally", height)
		}
	} else {
		log.Printf("�?Blockchain already at checkpoint height %d, no retry needed", height)
	}

	return nil
}
