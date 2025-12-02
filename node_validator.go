package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"fan-chain/core"
)

// 【P6协议】小弟启动即跟随
// 核心思路：
// 1. 网络正常时，小弟不产块，直接从大哥获取checkpoint和高度
// 2. 发现分叉时，小弟必须回退到分叉点之前，让大哥的链为准
// 3. 确保至少落后大哥1个区块后再激活，避免同时出块
func (n *Node) RequestValidatorActivation() error {
	if !n.isActiveValidator(n.address) {
		return fmt.Errorf("not a validator")
	}

	// 如果是创世节点（没有种子节点），直接激活
	if len(n.config.SeedPeers) == 0 {
		n.validatorActivated = true
		n.syncedHeight = n.chain.GetLatestHeight()
		log.Printf("✓ Genesis validator activated at height %d", n.syncedHeight)
		return nil
	}

	// 【P6.1】等待P2P连接建立
	if n.p2pServer == nil || n.p2pServer.GetPeerCount() == 0 {
		log.Printf("⏳ 【P6】等待P2P连接...")
		return fmt.Errorf("waiting for P2P connection")
	}

	// 【P6.2】获取大哥的高度
	bestPeerHeight := n.p2pServer.GetBestPeerHeight()
	myHeight := n.chain.GetLatestHeight()

	log.Printf("📊 【P6】小弟高度=%d, 大哥高度=%d", myHeight, bestPeerHeight)

	// 【P6.3】如果大哥高度更高，先同步checkpoint
	if bestPeerHeight > myHeight+2 {
		log.Printf("📡 【P6】小弟落后 %d 块，请求checkpoint同步...", bestPeerHeight-myHeight)
		n.p2pServer.RequestCheckpointFromBestPeer()
		time.Sleep(3 * time.Second)

		newHeight := n.chain.GetLatestHeight()
		if newHeight < bestPeerHeight-2 {
			log.Printf("⏳ 【P6】同步中: %d -> %d (目标: %d)", myHeight, newHeight, bestPeerHeight)
			return fmt.Errorf("syncing: height %d, target %d", newHeight, bestPeerHeight)
		}
		myHeight = newHeight
	}

	// 【P6.4】验证区块hash一致性（防止分叉）- 从最高到最低逐个检查
	if myHeight > 0 {
		forkDetected := false
		forkHeight := uint64(0)

		// 从当前高度往回检查，找到分叉点
		for checkHeight := myHeight; checkHeight > 0 && checkHeight > myHeight-10; checkHeight-- {
			localBlock, err := n.db.GetBlockByHeight(checkHeight)
			if err != nil || localBlock == nil {
				continue
			}
			localHash := localBlock.Hash().String()[:16]

			// 从大哥获取该高度的区块hash
			for _, seed := range n.config.SeedPeers {
				blockURL := fmt.Sprintf("http://%s/block/%d", strings.Replace(seed, ":9001", ":9000", 1), checkHeight)
				client := &http.Client{Timeout: 5 * time.Second}
				resp, err := client.Get(blockURL)
				if err != nil {
					continue
				}

				var blockInfo struct {
					Hash string `json:"hash"`
				}
				if json.NewDecoder(resp.Body).Decode(&blockInfo) == nil && blockInfo.Hash != "" {
					networkHash := blockInfo.Hash
					if len(networkHash) > 16 {
						networkHash = networkHash[:16]
					}

					if localHash != networkHash {
						log.Printf("🚨 【P6】分叉检测! 高度 %d: 本地=%s, 网络=%s", checkHeight, localHash, networkHash)
						forkDetected = true
						forkHeight = checkHeight
					} else {
						// hash一致，这个高度是安全的
						if forkDetected {
							log.Printf("✓ 【P6】找到分叉点: 高度 %d 一致，高度 %d 开始分叉", checkHeight, forkHeight)
						}
					}
				}
				resp.Body.Close()
				break
			}

			// 如果这个高度hash一致，且之前检测到分叉，说明找到了分叉点
			if !forkDetected {
				break // 没有分叉，不需要继续往回检查
			}
		}

		// 【P6.5】发现分叉，必须回退！
		if forkDetected && forkHeight > 0 {
			rollbackTo := forkHeight - 1
			if rollbackTo < 1 {
				rollbackTo = 1
			}
			log.Printf("🔄 【P6】小弟主动回退到高度 %d，放弃分叉区块!", rollbackTo)

			// 调用回退逻辑
			if err := n.rollbackToHeight(rollbackTo); err != nil {
				log.Printf("❌ 【P6】回退失败: %v", err)
			}

			// 请求大哥的checkpoint和区块
			n.p2pServer.RequestCheckpointFromBestPeer()
			time.Sleep(3 * time.Second)

			return fmt.Errorf("fork resolved, rolled back to %d, re-syncing", rollbackTo)
		}

		log.Printf("✓ 【P6】区块hash验证通过，无分叉")
	}

	// 【P6.6】高度对齐检查 - 必须至少落后大哥1个区块
	bestPeerHeight = n.p2pServer.GetBestPeerHeight() // 重新获取
	myHeight = n.chain.GetLatestHeight()

	// 【关键】小弟必须落后大哥至少1个区块才能激活
	if myHeight >= bestPeerHeight {
		log.Printf("⏳ 【P6】小弟高度=%d >= 大哥高度=%d，等待大哥出新块...", myHeight, bestPeerHeight)
		return fmt.Errorf("waiting for leader to produce block first")
	}

	if bestPeerHeight > myHeight+1 {
		log.Printf("⏳ 【P6】还差 %d 块，继续同步...", bestPeerHeight-myHeight)
		n.p2pServer.RequestSyncFromBestPeer(myHeight+1, bestPeerHeight)
		return fmt.Errorf("still syncing: %d blocks behind", bestPeerHeight-myHeight)
	}

	// 【P6.7】输出验证者集合信息
	validators := n.consensus.ValidatorSet().GetActiveValidators()
	log.Printf("📊 【P6】验证者集合: %d 个节点", len(validators))
	for i, v := range validators {
		isMe := ""
		if v.Address == n.address {
			isMe = " <- 我"
		}
		log.Printf("  [%d] %s: %d 质押%s", i+1, v.Address[:10], v.StakedAmount, isMe)
	}

	// 【P6.8】激活！小弟比大哥落后1个区块，安全激活
	n.validatorActivated = true
	n.syncedHeight = myHeight
	log.Printf("✓ 【P6】小弟就位! 高度=%d (大哥=%d)，准备竞争出块", n.syncedHeight, bestPeerHeight)

	return nil
}

// 【P6.10】回退到指定高度（删除分叉区块）
func (n *Node) rollbackToHeight(targetHeight uint64) error {
	currentHeight := n.chain.GetLatestHeight()
	if targetHeight >= currentHeight {
		return nil // 不需要回退
	}

	log.Printf("🔄 【P6】开始回退: %d -> %d", currentHeight, targetHeight)

	// 获取目标高度的区块
	targetBlock, err := n.db.GetBlockByHeight(targetHeight)
	if err != nil || targetBlock == nil {
		return fmt.Errorf("cannot find block at height %d", targetHeight)
	}

	// 更新链状态到目标区块
	n.chain.SetLatestBlock(targetBlock)

	// 请求checkpoint来恢复正确的状态
	n.p2pServer.RequestCheckpointFromBestPeer()

	log.Printf("✓ 【P6】回退完成，当前高度=%d", n.chain.GetLatestHeight())
	return nil
}

// 后台持续尝试激活验证者
func (n *Node) StartActivationMonitor() {
	go func() {
		// 【P6】快速重试：小弟要尽快跟上大哥
		retryInterval := 5 * time.Second

		for {
			time.Sleep(retryInterval)

			// 如果已经激活，退出循环
			if n.validatorActivated {
				return
			}

			// 如果不是验证者，退出
			if !n.isActiveValidator(n.address) {
				return
			}

			// 尝试激活
			if err := n.RequestValidatorActivation(); err != nil {
				log.Printf("【P6】激活重试: %v", err)
				// 如果是同步中，用更短的间隔重试
				if n.p2pServer != nil && n.p2pServer.GetPeerCount() > 0 {
					retryInterval = 3 * time.Second
				}
			} else {
				log.Printf("✓ 【P6】验证者激活成功!")
				return
			}
		}
	}()
}

// 【P5.1协议】检查是否应该进入孤立模式
// 只有在网络完全不可达时才孤立出块
func (n *Node) checkIsolatedMode() bool {
	if n.p2pServer == nil {
		return false
	}

	// 有P2P连接，不进入孤立模式
	if n.p2pServer.GetPeerCount() > 0 {
		return false
	}

	// 检查HTTP API是否可达
	for _, seed := range n.config.SeedPeers {
		url := fmt.Sprintf("http://%s/status", strings.Replace(seed, ":9001", ":9000", 1))
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return false // API可达，不进入孤立模式
		}
	}

	// 确认本节点是有效验证者
	validators := n.consensus.ValidatorSet().GetActiveValidators()
	for _, v := range validators {
		if v.Address == n.address && v.StakedAmount > 0 {
			log.Printf("🔥 【P5.1】网络完全不可达，进入孤立模式")
			n.isolatedMode = true
			return true
		}
	}

	return false
}

// 【P6.9】获取共识配置（供其他模块调用）
func (n *Node) getConsensusConfig() *core.ConsensusConfig {
	return core.GetConsensusConfig()
}
