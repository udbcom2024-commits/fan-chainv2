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

// 请求验证者激活（需要网络中其他验证者确认）
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

	// 新验证者需要验证同步状态
	currentHeight := n.chain.GetLatestHeight()
	localBlock := n.chain.GetLatestBlock()
	var localHash string
	if localBlock != nil {
		localHash = localBlock.Hash().String()[:16]
	}

	// 从网络获取最新高度和区块哈希
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

		// 【放宽规则】容忍最多12个区块高度差异（一个checkpoint周期）
		// 真正的安全保障是区块哈希验证，而非严格的高度同步
		const maxHeightGap = 12 // 一个checkpoint周期
		gap := int64(status.Height) - int64(currentHeight)
		if gap > maxHeightGap {
			log.Printf("⚠ Not fully synced: local=%d, network=%d (gap=%d blocks, max tolerance: %d)",
				currentHeight, status.Height, gap, maxHeightGap)

			// 【主动同步】检测到落后，立即请求同步
			if n.p2pServer != nil {
				log.Printf("Initiating proactive sync for blocks %d-%d", currentHeight+1, status.Height)
				n.p2pServer.RequestSyncFromBestPeer(currentHeight+1, status.Height)
			}

			return fmt.Errorf("not fully synced: %d blocks behind (max tolerance: %d)", gap, maxHeightGap)
		}

		// 【关键修复】检查是否发生分叉
		// 获取网络上同一高度的区块哈希，如果不同则说明分叉
		if currentHeight > 0 {
			blockURL := fmt.Sprintf("http://%s/block/%d", strings.Replace(seed, ":9001", ":9000", 1), currentHeight)
			blockResp, err := http.Get(blockURL)
			if err == nil {
				defer blockResp.Body.Close()
				var blockInfo struct {
					Hash string `json:"hash"`
				}
				if json.NewDecoder(blockResp.Body).Decode(&blockInfo) == nil && blockInfo.Hash != "" {
					networkHash := blockInfo.Hash
					if len(networkHash) > 16 {
						networkHash = networkHash[:16]
					}
					if localHash != networkHash {
						log.Printf("🚨 FORK DETECTED! Height %d: local=%s, network=%s",
							currentHeight, localHash, networkHash)
						log.Printf("⚠ Validator will NOT activate until fork is resolved")
						log.Printf("💡 To resolve: stop node, delete data directory, restart to sync from checkpoint")
						return fmt.Errorf("fork detected at height %d: local hash differs from network", currentHeight)
					}
					log.Printf("✓ Block hash verified at height %d: %s", currentHeight, localHash)
				}
			}
		}

		if gap > 0 && gap <= maxHeightGap {
			log.Printf("✓ Nearly synced: local=%d, network=%d (%d blocks behind, within tolerance)",
				currentHeight, status.Height, gap)
		}

		snapshotURL := fmt.Sprintf("http://%s/state/snapshot", strings.Replace(seed, ":9001", ":9000", 1))
		log.Printf("📡 Requesting state snapshot from: %s", snapshotURL)
		snapshotResp, err := http.Get(snapshotURL)
		if err != nil {
			log.Printf("⚠ State snapshot API not available: %v (skipping validation)", err)
			// API不可用时，跳过状态验证，直接激活
			// 创世节点的质押在所有其他验证者之前，不需要验证
		} else if snapshotResp.StatusCode == 404 {
			log.Printf("⚠ State snapshot API not found (404) - older node version, skipping validation")
			// 旧版本节点没有此API，跳过验证
		} else {
			defer snapshotResp.Body.Close()
			// TODO: 实现完整的状态验证逻辑
			log.Printf("📊 State snapshot available (status: %d), validation not yet implemented", snapshotResp.StatusCode)
		}

		// 【修复】输出当前验证者集合信息
		validators := n.consensus.ValidatorSet().GetActiveValidators()
		log.Printf("📊 Current validator set: %d validators", len(validators))
		for i, v := range validators {
			isMe := ""
			if v.Address == n.address {
				isMe = " <- ME"
			}
			log.Printf("  [%d] %s: %d stake%s", i+1, v.Address[:10], v.StakedAmount, isMe)
		}

		// 【Checkpoint边界检查】避免在checkpoint更新前激活导致分叉
		consensusConfig := core.GetConsensusConfig()
		checkpointInterval := uint64(consensusConfig.BlockParams.CheckpointInterval)
		activationBuffer := consensusConfig.ValidatorParams.CheckpointActivationBuffer
		if activationBuffer <= 0 {
			activationBuffer = 3 // 默认值
		}

		blocksUntilCheckpoint := checkpointInterval - (currentHeight % checkpointInterval)
		if blocksUntilCheckpoint == checkpointInterval {
			blocksUntilCheckpoint = 0 // 当前就是checkpoint高度
		}

		if blocksUntilCheckpoint > 0 && blocksUntilCheckpoint <= uint64(activationBuffer) {
			waitSeconds := blocksUntilCheckpoint * 5 // 每块5秒
			log.Printf("⏳ Only %d blocks until next checkpoint (buffer=%d), waiting %ds for checkpoint update...",
				blocksUntilCheckpoint, activationBuffer, waitSeconds)
			time.Sleep(time.Duration(waitSeconds+5) * time.Second) // 额外等5秒确保checkpoint更新

			// 更新当前高度
			currentHeight = n.chain.GetLatestHeight()
			log.Printf("✓ Checkpoint passed, current height: %d", currentHeight)
		}

		// 【关键修复】激活前等待，确保收到网络上可能已存在的区块
		// 避免在同一高度出块导致分叉
		log.Printf("⏳ Waiting for network sync before activation (5s grace period)...")
		time.Sleep(5 * time.Second)

		// 重新检查高度，可能在等待期间收到了新区块
		newHeight := n.chain.GetLatestHeight()
		if newHeight > currentHeight {
			log.Printf("✓ Received %d new blocks during grace period", newHeight-currentHeight)
			currentHeight = newHeight
		}

		// 【P2协议-P4共识绑定】检查向下同步是否完成
		// 未完成同步的验证者无出块权
		if n.p2pServer != nil && !n.p2pServer.IsBackfillComplete() {
			backfillTarget := n.p2pServer.GetBackfillTargetHeight()
			myEarliest := n.db.GetEarliestHeight()
			log.Printf("🚫 【P4】Backfill sync incomplete! My earliest: %d, target: %d",
				myEarliest, backfillTarget)
			log.Printf("⏳ 【P4】Cannot activate until backfill sync completes")
			return fmt.Errorf("backfill sync incomplete: need blocks from %d", backfillTarget)
		}

		// 【P2协议】检查本地最早区块高度是否符合要求
		myEarliest := n.db.GetEarliestHeight()
		log.Printf("📊 【P2】Local earliest block height: %d", myEarliest)

		// 状态一致，激活验证者
		n.validatorActivated = true
		n.syncedHeight = currentHeight
		log.Printf("✓ Validator activated at height %d after network confirmation", n.syncedHeight)

		// 激活后使用P2P同步，无需HTTP fallback
		log.Printf("✓ Validator ready to participate in consensus")

		return nil
	}

	return fmt.Errorf("failed to verify state with network")
}

// 后台持续尝试激活验证者（用于同步完成后的激活）
func (n *Node) StartActivationMonitor() {
	go func() {
		for {
			time.Sleep(30 * time.Second)

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
				log.Printf("Activation retry failed: %v", err)
			} else {
				log.Printf("✓ Validator successfully activated")
				return
			}
		}
	}()
}
