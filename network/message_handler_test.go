package network

import (
	"fan-chain/core"
	"log"
	"sort"
	"strings"
	"time"
)

// 消息处理循环
func (s *Server) messageLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.closeChan:
			return
		case <-ticker.C:
			s.processMessages()
		}
	}
}

// 处理消息
func (s *Server) processMessages() {
	s.peersMu.RLock()
	peers := make([]*Peer, 0, len(s.peers))
	for _, peer := range s.peers {
		peers = append(peers, peer)
	}
	s.peersMu.RUnlock()

	for _, peer := range peers {
		if !peer.IsConnected() {
			s.removePeer(peer.host)
			continue
		}

		// 非阻塞接收消�?
		select {
		case msg := <-peer.recvChan:
			s.handleMessage(peer, msg)
		default:
		}
	}
}

// 处理消息
func (s *Server) handleMessage(peer *Peer, msg *Message) {
	switch msg.Type {
	case MsgPing:
		s.handlePing(peer, msg)
	case MsgPong:
		s.handlePong(peer, msg)
	case MsgGetLatest:
		s.handleGetLatest(peer)
	case MsgLatestHeight:
		s.handleLatestHeight(peer, msg)
	case MsgGetBlocks:
		s.handleGetBlocks(peer, msg)
	case MsgBlocks:
		s.handleBlocks(peer, msg)
	case MsgNewBlock:
		s.handleNewBlock(peer, msg)
	case MsgTransaction:
		s.handleTransaction(peer, msg)
	case MsgGetCheckpoint:
		s.handleGetCheckpoint(peer, msg)
	case MsgCheckpoint:
		s.handleCheckpoint(peer, msg)
	case MsgGetState:
		s.handleGetState(peer, msg)
	case MsgStateData:
		s.handleStateData(peer, msg)
	case MsgGetEarliestHeight:
		s.handleGetEarliestHeight(peer, msg)
	case MsgEarliestHeight:
		s.handleEarliestHeight(peer, msg)
	default:
		log.Printf("Unknown message type from %s: %d", peer.host, msg.Type)
	}
}

// 处理Ping
func (s *Server) handlePing(peer *Peer, msg *Message) {
	var ping PingMessage
	if err := msg.ParsePayload(&ping); err != nil {
		log.Printf("Failed to parse ping from %s: %v", peer.host, err)
		return
	}

	// 🔒 共识校验：检查对方的共识参数是否一�?
	consensusConfig := core.GetConsensusConfig()
	if ping.ConsensusVersion != consensusConfig.ConsensusVersion || ping.ConsensusHash != consensusConfig.ConsensusHash {
		log.Printf("�?CONSENSUS MISMATCH from %s:", peer.host)
		log.Printf("   Local:  v%s hash=%s...", consensusConfig.ConsensusVersion, consensusConfig.ConsensusHash[:16])
		log.Printf("   Remote: v%s hash=%s...", ping.ConsensusVersion, ping.ConsensusHash[:16])
		log.Printf("   ⚠️  Disconnecting incompatible peer (different blockchain network)")
		s.removePeer(peer.host)
		return
	}

	log.Printf("Received ping from %s (height: %d, consensus: OK)", ping.Address, ping.Height)
	peer.SetAddress(ping.Address)

	// 回复Pong（包含共识信息和checkpoint信息�?
	var height uint64
	var blockHash string
	if s.getLatestBlock != nil {
		latestBlock := s.getLatestBlock()
		if latestBlock != nil {
			height = latestBlock.Header.Height
			blockHash = latestBlock.Hash().String()
		}
	}

	// 获取最新checkpoint信息
	var checkpointHeight uint64
	var checkpointHash string
	var checkpointTimestamp int64
	if s.getLatestCheckpoint != nil {
		checkpoint, err := s.getLatestCheckpoint()
		if err == nil && checkpoint != nil {
			checkpointHeight = checkpoint.Height
			checkpointHash = checkpoint.BlockHash.String()
			checkpointTimestamp = checkpoint.Timestamp
		}
	}

	pong := &PongMessage{
		Address:             s.address,
		Height:              height,
		LatestBlockHash:     blockHash,
		CheckpointHeight:    checkpointHeight,
		CheckpointHash:      checkpointHash,
		CheckpointTimestamp: checkpointTimestamp,
		ConsensusVersion:    consensusConfig.ConsensusVersion,
		ConsensusHash:       consensusConfig.ConsensusHash,
	}

	pongMsg, err := NewMessage(MsgPong, pong)
	if err != nil {
		log.Printf("Failed to create pong message: %v", err)
		return
	}

	peer.SendMessage(pongMsg)
}

// 处理Pong
func (s *Server) handlePong(peer *Peer, msg *Message) {
	var pong PongMessage
	if err := msg.ParsePayload(&pong); err != nil {
		log.Printf("Failed to parse pong from %s: %v", peer.host, err)
		return
	}

	// 🔒 共识校验：检查对方的共识参数是否一�?
	consensusConfig := core.GetConsensusConfig()
	if pong.ConsensusVersion != consensusConfig.ConsensusVersion || pong.ConsensusHash != consensusConfig.ConsensusHash {
		log.Printf("�?CONSENSUS MISMATCH from %s:", peer.host)
		log.Printf("   Local:  v%s hash=%s...", consensusConfig.ConsensusVersion, consensusConfig.ConsensusHash[:16])
		log.Printf("   Remote: v%s hash=%s...", pong.ConsensusVersion, pong.ConsensusHash[:16])
		log.Printf("   ⚠️  Disconnecting incompatible peer (different blockchain network)")
		s.removePeer(peer.host)
		return
	}

	log.Printf("Received pong from %s (height: %d, consensus: OK)", pong.Address, pong.Height)
	peer.SetAddress(pong.Address)
	peer.UpdateHeartbeat() // 更新心跳时间
	peer.SetHeight(pong.Height) // 【家长制】更新peer高度

	// 🔍 分叉检测：基于Checkpoint的分叉检测和解决（谁快认谁做大哥�?
	if s.detectAndResolveFork != nil {
		if err := s.detectAndResolveFork(pong.Height, pong.LatestBlockHash, pong.CheckpointHeight, pong.CheckpointHash, pong.CheckpointTimestamp); err != nil {
			log.Printf("⚠️  Fork detection/resolution failed: %v", err)
		}
	}
}

// 处理获取最新高�?
func (s *Server) handleGetLatest(peer *Peer) {
	var height uint64
	if s.getLatestBlock != nil {
		latestBlock := s.getLatestBlock()
		if latestBlock != nil {
			height = latestBlock.Header.Height
		}
	}

	msg, err := NewMessage(MsgLatestHeight, &LatestHeightMessage{Height: height})
	if err != nil {
		log.Printf("Failed to create latest height message: %v", err)
		return
	}

	peer.SendMessage(msg)
}

// 处理最新高�?
func (s *Server) handleLatestHeight(peer *Peer, msg *Message) {
	var latestHeight LatestHeightMessage
	if err := msg.ParsePayload(&latestHeight); err != nil {
		log.Printf("Failed to parse latest height from %s: %v", peer.host, err)
		return
	}

	log.Printf("Peer %s latest height: %d", peer.host, latestHeight.Height)
}

// 处理获取区块请求
func (s *Server) handleGetBlocks(peer *Peer, msg *Message) {
	var req GetBlocksMessage
	if err := msg.ParsePayload(&req); err != nil {
		log.Printf("Failed to parse get blocks from %s: %v", peer.host, err)
		return
	}

	log.Printf("Peer %s requesting blocks %d-%d", peer.host, req.FromHeight, req.ToHeight)

	// 从数据库获取区块
	if s.getBlockRange == nil {
		log.Printf("getBlockRange not configured")
		return
	}

	blocks, err := s.getBlockRange(req.FromHeight, req.ToHeight)
	if err != nil {
		log.Printf("Failed to get blocks %d-%d: %v", req.FromHeight, req.ToHeight, err)
		return
	}

	if len(blocks) == 0 {
		log.Printf("No blocks found in range %d-%d", req.FromHeight, req.ToHeight)
		return
	}

	// 发送区�?
	blocksMsg, err := NewMessage(MsgBlocks, &BlocksMessage{Blocks: blocks})
	if err != nil {
		log.Printf("Failed to create blocks message: %v", err)
		return
	}

	peer.SendMessage(blocksMsg)
	log.Printf("Sent %d blocks (%d-%d) to %s", len(blocks), req.FromHeight, req.ToHeight, peer.host)
}

// 处理区块数据
func (s *Server) handleBlocks(peer *Peer, msg *Message) {
	var blocks BlocksMessage
	if err := msg.ParsePayload(&blocks); err != nil {
		log.Printf("Failed to parse blocks from %s: %v", peer.host, err)
		s.finishSync()
		return
	}

	if len(blocks.Blocks) == 0 {
		log.Printf("Received 0 blocks from %s", peer.host)
		s.finishSync()
		return
	}

	log.Printf("Received %d blocks from %s (heights %d-%d)",
		len(blocks.Blocks), peer.host,
		blocks.Blocks[0].Header.Height,
		blocks.Blocks[len(blocks.Blocks)-1].Header.Height)

	// 【修复】获取当前本地高度，验证收到的区块是否匹配预�?
	var currentLocalHeight uint64
	if s.getLatestBlock != nil {
		latestBlock := s.getLatestBlock()
		if latestBlock != nil {
			currentLocalHeight = latestBlock.Header.Height
		}
	}

	// 【checkpoint同步修复】如果有checkpointSyncFrom，允许从该高度开始接受区�?
	s.syncMu.Lock()
	checkpointFrom := s.checkpointSyncFrom
	cpHeight := s.checkpointHeight
	// 【修复】如果当前高度已经超过checkpoint高度，说明历史回填已完成，清除checkpoint同步模式
	if checkpointFrom > 0 && currentLocalHeight >= cpHeight {
		log.Printf("📦 Checkpoint sync completed in handleBlocks: current height %d >= checkpoint height %d, clearing checkpoint mode", currentLocalHeight, cpHeight)
		s.checkpointSyncFrom = 0
		s.checkpointHeight = 0
		checkpointFrom = 0
	}
	s.syncMu.Unlock()

	// 确定实际的接受起�?
	acceptFromHeight := currentLocalHeight + 1
	if checkpointFrom > 0 && checkpointFrom < acceptFromHeight {
		// checkpoint同步模式：接受从checkpoint前一个周期开始的区块
		acceptFromHeight = checkpointFrom
		log.Printf("📦 Checkpoint sync mode: accepting blocks from height %d", acceptFromHeight)
	}

	// 【关键修复】先按高度排序，因为区块可能乱序到达
	sort.Slice(blocks.Blocks, func(i, j int) bool {
		return blocks.Blocks[i].Header.Height < blocks.Blocks[j].Header.Height
	})

	// 【家规修复】过滤区块：支持两种模式
	// 1. 正常同步模式：保留从acceptFromHeight开始的连续区块
	// 2. 分叉替换模式：处理高度小于当前高度的区块（用大哥的替换本地分叉）
	var filteredBlocks []*core.Block
	var forkedBlocks []*core.Block // 需要替换的分叉区块

	expectedNextHeight := acceptFromHeight
	for _, block := range blocks.Blocks {
		// 【家规】检查是否是分叉替换区块（高度小于当前本地高度）
		if block.Header.Height <= currentLocalHeight && block.Header.Height > 0 {
			// 这是用于替换分叉的区块，单独收集
			forkedBlocks = append(forkedBlocks, block)
			continue
		}

		// 正常同步：跳过已有的区块（小于acceptFromHeight�?
		if block.Header.Height < acceptFromHeight {
			continue
		}
		if block.Header.Height == expectedNextHeight {
			filteredBlocks = append(filteredBlocks, block)
			expectedNextHeight++
		} else if block.Header.Height > expectedNextHeight {
			// 跳过了一些区块，记录日志但继�?
			log.Printf("⚠️  Block #%d skipped expected #%d, stopping filter", block.Header.Height, expectedNextHeight)
			break
		}
	}

	// 【家规】处理分叉替换区�?
	if len(forkedBlocks) > 0 && s.replaceForkedBlock != nil {
		log.Printf("🔧 【家规】Received %d forked blocks for replacement (heights %d-%d)",
			len(forkedBlocks), forkedBlocks[0].Header.Height, forkedBlocks[len(forkedBlocks)-1].Header.Height)
		for _, block := range forkedBlocks {
			if err := s.replaceForkedBlock(block); err != nil {
				log.Printf("�?【家规】Failed to replace forked block #%d: %v", block.Header.Height, err)
			} else {
				log.Printf("�?【家规】Replaced forked block #%d with big brother's version", block.Header.Height)
			}
		}
	}

	// 【P2协议】检查是否是向下同步（backfill）的区块
	s.syncMu.Lock()
	isBackfill := s.backfillInProgress
	backfillTarget := s.backfillTargetHeight
	s.syncMu.Unlock()

	if isBackfill && len(blocks.Blocks) > 0 {
		// 检查收到的区块是否在checkpoint以下（向下同步的区块�?
		firstBlockHeight := blocks.Blocks[0].Header.Height
		lastBlockHeight := blocks.Blocks[len(blocks.Blocks)-1].Header.Height

		if lastBlockHeight < cpHeight && s.saveBlockOnly != nil {
			// 这是向下同步的区块，只保存不执行
			log.Printf("📦 【P2-Backfill】Processing %d backfill blocks (heights %d-%d)",
				len(blocks.Blocks), firstBlockHeight, lastBlockHeight)

			savedCount := 0
			var lowestSaved uint64 = ^uint64(0) // 初始化为最大�?
			for _, block := range blocks.Blocks {
				if block.Header.Height < cpHeight {
					if err := s.saveBlockOnly(block); err != nil {
						log.Printf("⚠️ 【P2-Backfill】Failed to save block #%d: %v", block.Header.Height, err)
					} else {
						savedCount++
						if block.Header.Height < lowestSaved {
							lowestSaved = block.Header.Height
						}
					}
				}
			}

			log.Printf("�?【P2-Backfill】Saved %d blocks, lowest: %d", savedCount, lowestSaved)

			// 检查是否需要继续向下同�?
			if lowestSaved > backfillTarget {
				// 还需要继续向�?
				nextFrom := lowestSaved - 1
				log.Printf("📦 【P2-Backfill】Continuing backfill from %d to %d", nextFrom, backfillTarget)
				s.requestBackfillBatch(peer, nextFrom, backfillTarget)
			} else {
				// 向下同步完成
				s.syncMu.Lock()
				s.backfillInProgress = false
				s.syncMu.Unlock()
				log.Printf("�?【P2协议】Backfill sync completed! Earliest block: %d", lowestSaved)
			}
			return // 向下同步的区块处理完毕，不继续执行正常同步逻辑
		}
	}

	if len(filteredBlocks) == 0 {
		log.Printf("⚠️  No usable blocks after filtering (expected height %d, got %d-%d from peer)",
			acceptFromHeight,
			blocks.Blocks[0].Header.Height,
			blocks.Blocks[len(blocks.Blocks)-1].Header.Height)
		// 重新请求正确范围的区�?
		s.syncMu.Lock()
		targetHeight := s.syncTargetHeight
		syncPeer := s.syncPeer
		s.syncMu.Unlock()
		if targetHeight > 0 {
			s.requestNextBatch(syncPeer, acceptFromHeight)
		}
		return
	}

	log.Printf("�?Filtered to %d usable blocks (heights %d-%d)",
		len(filteredBlocks),
		filteredBlocks[0].Header.Height,
		filteredBlocks[len(filteredBlocks)-1].Header.Height)

	// 添加区块到本地链
	var lastAddedHeight uint64
	if s.addBlock != nil {
		// 判断是否在同步历史区块（区块时间戳与当前时间差超�?分钟�?
		isHistoricalSync := len(filteredBlocks) > 0 && filteredBlocks[0].Header.Height <= currentLocalHeight+100

		// 获取checkpoint高度，用于判断是否需要回填历史区�?
		s.syncMu.Lock()
		cpHeight := s.checkpointHeight
		s.syncMu.Unlock()

		for i, block := range filteredBlocks {
			var err error

			// 【checkpoint回填】如果区块高度小于checkpoint高度，只保存到数据库，不更新链状�?
			if cpHeight > 0 && block.Header.Height < cpHeight && s.saveBlockOnly != nil {
				err = s.saveBlockOnly(block)
				if err == nil {
					log.Printf("📦 Backfilled block #%d (checkpoint history)", block.Header.Height)
					lastAddedHeight = block.Header.Height
					// 更新checkpointSyncFrom到下一个高�?
					s.syncMu.Lock()
					if block.Header.Height >= s.checkpointSyncFrom {
						s.checkpointSyncFrom = block.Header.Height + 1
					}
					s.syncMu.Unlock()
					continue
				}
				// 如果保存失败，记录错误但继续
				log.Printf("⚠️ Failed to backfill block #%d: %v", block.Header.Height, err)
				continue
			}

			// 对于历史区块同步，使用skipTimestamp版本
			if isHistoricalSync && s.addBlockSkipTimestamp != nil {
				err = s.addBlockSkipTimestamp(block)
			} else {
				err = s.addBlock(block)
			}

			if err != nil {
				// Check fork: first block fails with proposer/hash error
				if i == 0 && s.performReorg != nil {
					errMsg := err.Error()
					if strings.Contains(errMsg, "invalid previous hash") || strings.Contains(errMsg, "proposer verification failed") {
						rollbackHeight := block.Header.Height - 1
						log.Printf("🔄 FORK at #%d, reorg to #%d", block.Header.Height, rollbackHeight)
						if reorgErr := s.performReorg(rollbackHeight, block); reorgErr != nil {
							reorgErrMsg := reorgErr.Error()
							// 【家规】检测到深度分叉，触发向大哥请求正确区块
							if strings.HasPrefix(reorgErrMsg, "DEEP_FORK:") {
								log.Printf("🔍 【家规】DEEP FORK detected, requesting correct blocks from big brother...")
								// 解析分叉高度，从更早的位置开始请�?
								// 每次往回退100个区块，直到找到共同祖先
								deepSyncFrom := rollbackHeight
								if deepSyncFrom > 100 {
									deepSyncFrom = deepSyncFrom - 100
								} else {
									deepSyncFrom = 1
								}
								log.Printf("📡 【家规】Requesting blocks from height %d to find common ancestor", deepSyncFrom)
								go s.RequestBlocksDirect(deepSyncFrom, rollbackHeight)
							} else {
								log.Printf("�?REORG FAILED: %v", reorgErr)
							}
						} else {
							log.Printf("�?REORG SUCCESS")
							lastAddedHeight = block.Header.Height
						}
						continue
					}
					// Height mismatch: 主动向大哥请求缺失的区块
					if strings.Contains(errMsg, "invalid height") {
						// 解析期望的高�?
						var expectedHeight uint64
						if s.getLatestBlock != nil {
							latestBlock := s.getLatestBlock()
							if latestBlock != nil {
								expectedHeight = latestBlock.Header.Height + 1
							}
						}

						// 如果收到的区块高度比期望高度大，说明中间缺失了区�?
						if block.Header.Height > expectedHeight && expectedHeight > 0 {
							log.Printf("📡 Height gap detected! Expected #%d but got #%d, requesting missing blocks from big brother",
								expectedHeight, block.Header.Height)
							// 主动请求缺失的区�?
							go s.RequestBlocksDirect(expectedHeight, block.Header.Height-1)
						}
						continue
					}
				}
				log.Printf("Failed to add block #%d: %v", block.Header.Height, err)
				// 添加失败不中断同步，继续尝试后续区块
			} else {
				log.Printf("�?Added block #%d from peer", block.Header.Height)
				lastAddedHeight = block.Header.Height
			}
		}
	}

	// 检查是否需要继续同�?
	s.syncMu.Lock()
	syncing := s.syncing
	targetHeight := s.syncTargetHeight
	syncPeer := s.syncPeer
	s.syncMu.Unlock()

	if syncing && lastAddedHeight > 0 && lastAddedHeight < targetHeight {
		// 还需要继续同步下一�?
		nextHeight := lastAddedHeight + 1
		log.Printf("Continuing sync: next batch from height %d (target: %d)", nextHeight, targetHeight)
		s.requestNextBatch(syncPeer, nextHeight)
	} else if syncing && lastAddedHeight == 0 {
		// 所有区块都添加失败，检查是否已经同步到最�?
		var currentHeight uint64
		if s.getLatestBlock != nil {
			latestBlock := s.getLatestBlock()
			if latestBlock != nil {
				currentHeight = latestBlock.Header.Height
			}
		}

		// 【修复】如果当前高度已经达到或接近目标高度，停止同�?
		// 避免无限循环请求未来不存在的区块
		if currentHeight >= targetHeight || currentHeight+5 >= targetHeight {
			log.Printf("�?Sync complete: current height %d is near target %d, stopping sync", currentHeight, targetHeight)
			s.finishSync()
		} else {
			nextHeight := currentHeight + 1
			log.Printf("�?All blocks failed to add, retrying from local height %d+1=%d (target: %d)",
				currentHeight, nextHeight, targetHeight)
			s.requestNextBatch(syncPeer, nextHeight)
		}
	} else if syncing {
		// 同步完成
		s.finishSync()
		log.Printf("All blocks synced up to height %d", lastAddedHeight)
	}
}

// 处理新区�?
func (s *Server) handleNewBlock(peer *Peer, msg *Message) {
	var newBlock NewBlockMessage
	if err := msg.ParsePayload(&newBlock); err != nil {
		log.Printf("Failed to parse new block from %s: %v", peer.host, err)
		return
	}

	log.Printf("Received new block #%d from %s", newBlock.Block.Header.Height, peer.host)

	// 获取当前高度
	var currentHeight uint64
	if s.getLatestBlock != nil {
		latestBlock := s.getLatestBlock()
		if latestBlock != nil {
			currentHeight = latestBlock.Header.Height
		}
	}

	incomingHeight := newBlock.Block.Header.Height

	// 情况1: 区块是下一个区块（高度 = 当前高度 + 1�?
	if incomingHeight == currentHeight+1 {
		// 【分叉预防】验证proposer是否为VRF选中的合法proposer
		if s.verifyProposer != nil {
			latestBlock := s.getLatestBlock()
			if latestBlock != nil {
				expectedProposer, err := s.verifyProposer(incomingHeight, latestBlock)
				if err != nil {
					log.Printf("Failed to verify proposer for block #%d: %v", incomingHeight, err)
					return
				}
				if newBlock.Block.Header.Proposer != expectedProposer {
					log.Printf("�?FORK PREVENTED: Block #%d from %s rejected (VRF selected: %s)",
						incomingHeight, newBlock.Block.Header.Proposer, expectedProposer)
					return
				}
			}
		}

		if s.addBlock != nil {
			if err := s.addBlock(newBlock.Block); err != nil {
				log.Printf("Failed to add new block #%d: %v", incomingHeight, err)
			} else {
				log.Printf("�?Added new block #%d from peer", incomingHeight)
			}
		}
		return
	}

	// 情况1.5: 区块高度相同（currentHeight），可能是链重组
	if incomingHeight == currentHeight && currentHeight > 0 {
		// 检查是否来自VRF选定的正确proposer
		if s.verifyProposer != nil && currentHeight > 1 {
			// 获取前一个区块来验证VRF
			prevBlock, err := s.getBlockRange(currentHeight-1, currentHeight-1)
			if err != nil || len(prevBlock) == 0 {
				log.Printf("Failed to get previous block for reorg verification")
				return
			}

			expectedProposer, err := s.verifyProposer(currentHeight, prevBlock[0])
			if err != nil {
				log.Printf("Failed to verify proposer for reorg at #%d: %v", currentHeight, err)
				return
			}

			// 获取当前高度的本地区�?
			localBlocks, err := s.getBlockRange(currentHeight, currentHeight)
			if err != nil || len(localBlocks) == 0 {
				log.Printf("Failed to get local block for reorg comparison")
				return
			}
			localBlock := localBlocks[0]

			incomingProposer := newBlock.Block.Header.Proposer
			localProposer := localBlock.Header.Proposer

			// 情况A: 接收到的区块来自正确proposer，本地区块不�?-> 执行reorg
			if incomingProposer == expectedProposer && localProposer != expectedProposer {
				log.Printf("�?CHAIN REORGANIZATION: Local block #%d from wrong proposer %s, accepting correct block from %s",
					currentHeight, localProposer[:10], expectedProposer[:10])

				if s.performReorg != nil {
					if err := s.performReorg(currentHeight-1, newBlock.Block); err != nil {
						log.Printf("�?REORG FAILED: %v", err)
						return
					}
					log.Printf("�?REORG SUCCESS: Chain reorganized to height %d", currentHeight)
				} else {
					log.Printf("�?REORG NEEDED: performReorg callback not set")
				}
				return
			}

			// 情况B: 接收到的区块来自错误proposer，本地区块是正确�?-> 拒绝
			if incomingProposer != expectedProposer && localProposer == expectedProposer {
				log.Printf("�?FORK PREVENTED: Rejecting block #%d from wrong proposer %s (local has correct: %s)",
					currentHeight, incomingProposer[:10], expectedProposer[:10])
				return
			}

			// 情况C: 两个都不是VRF选中的正确proposer -> 使用质押权重选择
			if incomingProposer != expectedProposer && localProposer != expectedProposer {
				log.Printf("�?FORK DETECTED: Neither proposer matches VRF selection at height %d", currentHeight)
				log.Printf("   Local: %s, Incoming: %s, Expected: %s",
					localProposer[:10], incomingProposer[:10], expectedProposer[:10])

				// 使用质押权重选择�?
				if s.getProposerStake != nil {
					localStake := s.getProposerStake(localProposer)
					incomingStake := s.getProposerStake(incomingProposer)

					log.Printf("   Stake comparison: Local=%d FAN, Incoming=%d FAN",
						localStake/1000000, incomingStake/1000000)

					// 接收质押更高的链
					if incomingStake > localStake {
						log.Printf("�?STAKE-WEIGHTED REORG: Accepting block from higher-stake proposer")
						if s.performReorg != nil {
							if err := s.performReorg(currentHeight-1, newBlock.Block); err != nil {
								log.Printf("�?REORG FAILED: %v", err)
								return
							}
							log.Printf("�?REORG SUCCESS: Switched to higher-stake chain")
						}
						return
					} else if incomingStake < localStake {
						log.Printf("�?FORK RESOLVED: Keeping local block from higher-stake proposer")
						return
					} else {
						// 质押相同，使用VRF输出作为tie-breaker
						log.Printf("   Stakes equal, using VRF output as tie-breaker")
						// 比较proposer地址的字典序
						if incomingProposer > localProposer {
							log.Printf("�?TIE-BREAKER REORG: Accepting block based on VRF comparison")
							if s.performReorg != nil {
								if err := s.performReorg(currentHeight-1, newBlock.Block); err != nil {
									log.Printf("�?REORG FAILED: %v", err)
									return
								}
								log.Printf("�?REORG SUCCESS: Tie-breaker resolved")
							}
							return
						} else {
							log.Printf("�?TIE-BREAKER: Keeping local block based on VRF comparison")
							return
						}
					}
				} else {
					log.Printf("�?Cannot resolve fork: getProposerStake callback not set")
				}
				return
			}
		}

		// 其他情况：忽略相同高度的区块
		return
	}

	// 情况2: 区块高度过低（已经有了）
	if incomingHeight <= currentHeight {
		// 忽略旧区�?
		return
	}

	// 情况3: 区块高度跳跃（高�?> 当前高度 + 1），需要同�?
	if incomingHeight > currentHeight+1 {
		gap := incomingHeight - currentHeight - 1
		log.Printf("Height gap detected: current=%d, incoming=%d, gap=%d blocks",
			currentHeight, incomingHeight, gap)

		// 【修复】检查是否已经在同步中，避免与checkpoint同步冲突
		s.syncMu.Lock()
		alreadySyncing := s.syncing
		s.syncMu.Unlock()

		if alreadySyncing {
			log.Printf("⏸️  Sync already in progress, skipping gap sync request")
			return
		}

		// 【完整同步模式】新节点请求全部历史区块�?00天保留期�?
		// 不再强制使用Ephemeral checkpoint同步
		if currentHeight <= 1 {
			// 新节点：先请求checkpoint获取状态，然后请求完整区块历史
			log.Printf("📦 【Full Sync】New node requesting full block history")
			// 首先请求checkpoint来初始化状�?
			s.RequestCheckpointFromPeers(3)
			// 同时也请求历史区块（从peer最早的区块开始）
			// 注意：实际的起始高度取决于peer保留的区块（100天内�?
		}

		// 请求完整区块同步
		// 检查是否有checkpoint同步起点（从checkpoint前一个周期开始）
		s.syncMu.Lock()
		checkpointFrom := s.checkpointSyncFrom
		cpHeight := s.checkpointHeight
		// 【修复】如果当前高度已经超过checkpoint高度，说明历史回填已完成，清除checkpoint同步模式
		if checkpointFrom > 0 && currentHeight >= cpHeight {
			log.Printf("📦 Checkpoint sync completed: current height %d >= checkpoint height %d, clearing checkpoint mode", currentHeight, cpHeight)
			s.checkpointSyncFrom = 0
			s.checkpointHeight = 0
			checkpointFrom = 0
		}
		s.syncMu.Unlock()

		syncFrom := currentHeight + 1
		if checkpointFrom > 0 && checkpointFrom < syncFrom {
			// 使用checkpoint前一个周期作为同步起�?
			syncFrom = checkpointFrom
			log.Printf("📦 Using checkpoint sync start: %d (checkpoint - interval)", syncFrom)
		}
		if syncFrom == 1 {
			syncFrom = 1 // 确保从区�?开始（创世块之后）
		}
		s.requestSync(peer, syncFrom, incomingHeight-1)
	}
}

// 处理交易广播
func (s *Server) handleTransaction(peer *Peer, msg *Message) {
	var txMsg TransactionMessage
	if err := msg.ParsePayload(&txMsg); err != nil {
		log.Printf("Failed to parse transaction from %s: %v", peer.host, err)
		return
	}

	tx := txMsg.Transaction
	log.Printf("Received transaction %x from %s", tx.Hash().Bytes()[:8], peer.host)

	// 提交到本地节点处�?
	if s.handleReceivedTransaction != nil {
		if err := s.handleReceivedTransaction(tx); err != nil {
			log.Printf("Failed to handle received transaction: %v", err)
		}
	}
}

// 【P2协议】处理获取最早区块高度请�?
func (s *Server) handleGetEarliestHeight(peer *Peer, msg *Message) {
	log.Printf("📡 【P2】Peer %s requesting earliest block height", peer.host)

	var earliestHeight uint64 = 1 // 默认�?

	// 调用回调获取本节点最早区块高�?
	if s.getEarliestHeight != nil {
		earliestHeight = s.getEarliestHeight()
	}

	// 发送响�?
	respMsg, err := NewMessage(MsgEarliestHeight, &EarliestHeightMessage{
		Height: earliestHeight,
	})
	if err != nil {
		log.Printf("Failed to create earliest height message: %v", err)
		return
	}

	peer.SendMessage(respMsg)
	log.Printf("📡 【P2】Sent earliest height %d to %s", earliestHeight, peer.host)
}

// 【P2协议】处理最早区块高度响�?
func (s *Server) handleEarliestHeight(peer *Peer, msg *Message) {
	var earliest EarliestHeightMessage
	if err := msg.ParsePayload(&earliest); err != nil {
		log.Printf("Failed to parse earliest height from %s: %v", peer.host, err)
		return
	}

	log.Printf("📡 【P2】Received earliest height %d from big brother %s", earliest.Height, peer.host)

	s.syncMu.Lock()
	cpHeight := s.checkpointHeight

	// 如果没有checkpoint高度，说明还没完成checkpoint同步
	if cpHeight == 0 {
		s.syncMu.Unlock()
		log.Printf("⚠️ 【P2】No checkpoint height set, skipping backfill")
		return
	}

	// 设置向下同步目标
	s.backfillTargetHeight = earliest.Height
	s.backfillInProgress = true

	// 从checkpoint-1开始向下同�?
	startFrom := cpHeight - 1
	if startFrom < earliest.Height {
		// 已经同步完成
		s.backfillInProgress = false
		s.syncMu.Unlock()
		log.Printf("�?【P2】Backfill already complete (checkpoint %d, earliest %d)", cpHeight, earliest.Height)
		return
	}

	s.backfillCurrentFrom = startFrom
	s.syncMu.Unlock()

	log.Printf("📦 【P2】Starting backfill sync: from %d down to %d (checkpoint: %d)",
		startFrom, earliest.Height, cpHeight)

	// 开始向下分批请求区�?
	s.requestBackfillBatch(peer, startFrom, earliest.Height)
}

// 【P2协议】向下分批请求区�?
func (s *Server) requestBackfillBatch(peer *Peer, fromHeight, targetHeight uint64) {
	batchSize := uint64(100) // 每批100个区�?

	// 计算本批次范围（向下�?
	var batchStart uint64
	if fromHeight > batchSize {
		batchStart = fromHeight - batchSize + 1
	} else {
		batchStart = 1
	}

	// 确保不低于目标高�?
	if batchStart < targetHeight {
		batchStart = targetHeight
	}

	log.Printf("📦 【P2-Backfill】Requesting blocks %d-%d (target: %d)",
		batchStart, fromHeight, targetHeight)

	// 发送GetBlocks请求
	req := &GetBlocksMessage{
		FromHeight: batchStart,
		ToHeight:   fromHeight,
	}

	reqMsg, err := NewMessage(MsgGetBlocks, req)
	if err != nil {
		log.Printf("Failed to create backfill get blocks message: %v", err)
		return
	}

	if err := peer.SendMessage(reqMsg); err != nil {
		log.Printf("Failed to send backfill block request: %v", err)
		return
	}

	// 更新状�?
	s.syncMu.Lock()
	s.backfillCurrentFrom = batchStart - 1
	s.syncMu.Unlock()
}

