package main

import (
	"fan-chain/core"
	"fmt"
	"log"
	"os"
	"sync"
)

// 【认准真大哥】记住目前已知的最高checkpoint高度
// 避免被低checkpoint的小弟误导，越砍越低
var (
	knownHighestCheckpointHeight uint64
	knownHighestCheckpointHash   string
	knownHighestMu               sync.Mutex
)

// 基于Checkpoint的分叉解决方案
// 核心原则：
// 1. 信任Checkpoint - Checkpoint已经通过14亿总额验证且由权威节点签名
// 2. 拒绝Checkpoint之后的分叉 - 因为100天数据修剪后无法追溯
// 3. 比对Checkpoint Hash - 不一致则拒绝同步

// DetectAndResolveFork 检测并解决分叉（基于Checkpoint）
// peerHeight: 对方节点的高度
// peerBlockHash: 对方节点在该高度的区块哈希
// peerLatestCheckpointHeight: 对方最新checkpoint高度
// peerLatestCheckpointHash: 对方最新checkpoint的哈希
// peerCheckpointTimestamp: 对方最新checkpoint的时间戳（用于分叉选择：谁快认谁做大哥）
func (n *Node) DetectAndResolveFork(peerHeight uint64, peerBlockHash string, peerLatestCheckpointHeight uint64, peerLatestCheckpointHash string, peerCheckpointTimestamp int64) error {
	// 【认准真大哥-早期更新】第一时间更新已知最高checkpoint
	// 不管后续走哪条路，都要记住见过的最高checkpoint！
	if peerLatestCheckpointHeight > 0 {
		knownHighestMu.Lock()
		if peerLatestCheckpointHeight > knownHighestCheckpointHeight {
			knownHighestCheckpointHeight = peerLatestCheckpointHeight
			knownHighestCheckpointHash = peerLatestCheckpointHash
			log.Printf("📈 [早期更新]已知最高checkpoint: 高度=%d (来自peer)", peerLatestCheckpointHeight)
		}
		knownHighestMu.Unlock()
	}

	// 检查链是否已初始化
	if n.chain == nil {
		// 新节点还未初始化链，跳过分叉检测
		return nil
	}

	myHeight := n.chain.GetLatestHeight()
	myBlock := n.chain.GetLatestBlock()
	if myBlock == nil {
		// 链为空，跳过分叉检测
		return nil
	}
	myBlockHash := myBlock.Hash().String()

	// 如果对方没有区块哈希或checkpoint，跳过分叉检测
	if peerBlockHash == "" || peerLatestCheckpointHeight == 0 {
		return nil
	}

	// 获取本地最新的checkpoint
	myLatestCheckpoint, err := n.db.GetLatestCheckpoint(n.config.DataDir)
	if err != nil || myLatestCheckpoint == nil {
		// 如果本地没有checkpoint，可能是刚启动的新节点，允许同步
		return nil
	}

	myCheckpointHeight := myLatestCheckpoint.Height
	myCheckpointHash := myLatestCheckpoint.BlockHash.String()

	// 【认准真大哥-早期更新】也要记录本地checkpoint高度
	knownHighestMu.Lock()
	if myCheckpointHeight > knownHighestCheckpointHeight {
		knownHighestCheckpointHeight = myCheckpointHeight
		knownHighestCheckpointHash = myCheckpointHash
		log.Printf("📈 [早期更新]已知最高checkpoint: 高度=%d (来自本地)", myCheckpointHeight)
	}
	knownHighestMu.Unlock()

	// 情况1：高度相同，哈希不同 → 发生分叉
	if peerHeight == myHeight && peerBlockHash != myBlockHash {
		log.Printf("🚨 检测到分叉！高度=%d", myHeight)
		log.Printf("   本地区块哈希: %s", myBlockHash[:16])
		log.Printf("   对方区块哈希: %s", peerBlockHash[:16])

		// 获取本地checkpoint时间戳
		myCheckpointTimestamp := myLatestCheckpoint.Timestamp
		return n.ResolveForkUsingCheckpoint(myCheckpointHeight, myCheckpointHash, myCheckpointTimestamp,
			peerLatestCheckpointHeight, peerLatestCheckpointHash, peerCheckpointTimestamp)
	}

	// 情况2：对方更高 → 可能分叉，需要验证checkpoint
	if peerHeight > myHeight {
		// 检查对方的checkpoint是否与我们一致
		if peerLatestCheckpointHeight > 0 && myCheckpointHeight > 0 {
			// 比较共同高度的checkpoint
			commonCheckpointHeight := peerLatestCheckpointHeight
			if myCheckpointHeight < peerLatestCheckpointHeight {
				commonCheckpointHeight = myCheckpointHeight
			}

			// 获取共同高度的checkpoint进行比对
			myCheckpointAtHeight, err := n.db.LoadCheckpoint(commonCheckpointHeight, n.config.DataDir)
			if err == nil {
				myHashAtCommon := myCheckpointAtHeight.BlockHash.String()
				// 如果latest checkpoint高度相同但hash不同，说明分叉
				// 【家务事】不是简单拒绝，而是调用分叉解决逻辑，谁快认谁做大哥！
				if commonCheckpointHeight == peerLatestCheckpointHeight &&
					commonCheckpointHeight == myCheckpointHeight &&
					peerLatestCheckpointHash != myHashAtCommon {
					log.Printf("🚨 Checkpoint不一致，启动分叉解决！")
					log.Printf("   高度: %d", commonCheckpointHeight)
					log.Printf("   本地hash: %s", myHashAtCommon[:16])
					log.Printf("   对方hash: %s", peerLatestCheckpointHash[:16])
					// 获取本地checkpoint时间戳
					myCheckpointTimestamp := myCheckpointAtHeight.Timestamp
					// 调用分叉解决：谁快认谁做大哥！
					return n.ResolveForkUsingCheckpoint(myCheckpointHeight, myHashAtCommon, myCheckpointTimestamp,
						peerLatestCheckpointHeight, peerLatestCheckpointHash, peerCheckpointTimestamp)
				}

				// 【家务事】checkpoint高度一样且hash一致，但对方区块更高
				// 说明对方更快，我方落后了，直接清除多余区块跟上去！
				if commonCheckpointHeight == peerLatestCheckpointHeight &&
					commonCheckpointHeight == myCheckpointHeight &&
					peerLatestCheckpointHash == myHashAtCommon &&
					peerHeight > myHeight {
					log.Printf("✓ Checkpoint一致，对方区块更高（%d > %d），直接跟上去！", peerHeight, myHeight)
					// 不需要清除，正常同步即可
					return nil
				}
			}

			// 【正常同步】对方checkpoint更高，说明对方有更新的checkpoint
			// 这不是分叉，是正常的同步落后，直接正常同步即可
			if peerLatestCheckpointHeight > myCheckpointHeight {
				log.Printf("📦 对方Checkpoint更高（%d > %d），本地落后，正常同步即可", peerLatestCheckpointHeight, myCheckpointHeight)
				return nil
			}
		}

		log.Printf("对方节点高度更高且checkpoint一致，可以同步：本地=%d, 对方=%d", myHeight, peerHeight)
		return nil
	}

	// 情况3：我方更高 → 无需处理
	return nil
}

// ResolveForkUsingCheckpoint 使用Checkpoint解决分叉
// 核心规则：只有checkpoint高度相同时，才比较谁先出块来决定大哥
// 【家务事】发现对方更强时，直接清除自己的数据，强行重新同步！
// 【认准真大哥】在砍自己之前，先确认对方是否真的是大哥（checkpoint高度不低于已知最高）
func (n *Node) ResolveForkUsingCheckpoint(myCheckpointHeight uint64, myCheckpointHash string, myCheckpointTimestamp int64,
	peerCheckpointHeight uint64, peerCheckpointHash string, peerCheckpointTimestamp int64) error {

	log.Printf("🔍 使用Checkpoint解决分叉")
	log.Printf("   本地Checkpoint: 高度=%d, 时间戳=%d, hash=%s", myCheckpointHeight, myCheckpointTimestamp, myCheckpointHash[:16])
	log.Printf("   对方Checkpoint: 高度=%d, 时间戳=%d, hash=%s", peerCheckpointHeight, peerCheckpointTimestamp, peerCheckpointHash[:16])

	// 【认准真大哥】更新已知最高checkpoint
	knownHighestMu.Lock()
	// 如果对方checkpoint更高，更新已知最高
	if peerCheckpointHeight > knownHighestCheckpointHeight {
		knownHighestCheckpointHeight = peerCheckpointHeight
		knownHighestCheckpointHash = peerCheckpointHash
		log.Printf("📈 更新已知最高checkpoint: 高度=%d", peerCheckpointHeight)
	}
	// 如果本地checkpoint更高，也要更新
	if myCheckpointHeight > knownHighestCheckpointHeight {
		knownHighestCheckpointHeight = myCheckpointHeight
		knownHighestCheckpointHash = myCheckpointHash
		log.Printf("📈 更新已知最高checkpoint（本地）: 高度=%d", myCheckpointHeight)
	}
	currentKnownHighest := knownHighestCheckpointHeight
	knownHighestMu.Unlock()

	// 规则1：checkpoint高度不同
	if peerCheckpointHeight != myCheckpointHeight {
		// 【关键修复】对方checkpoint比我低，绝对不跟随！
		if peerCheckpointHeight < myCheckpointHeight {
			log.Printf("⚠️ 对方Checkpoint更低（对方=%d < 本地=%d），拒绝跟随！", peerCheckpointHeight, myCheckpointHeight)
			return nil
		}
		// 对方checkpoint比我高，正常同步即可
		log.Printf("📦 Checkpoint高度不同（本地=%d, 对方=%d），对方更高，正常同步", myCheckpointHeight, peerCheckpointHeight)
		return nil
	}

	// 规则2：checkpoint高度相同，但hash不同 → 发生分叉，比较谁先出块
	// 这是真正的分叉情况：同一高度产生了不同的checkpoint
	if peerCheckpointHash != myCheckpointHash {
		log.Printf("🚨 同高度Checkpoint hash不同，发生分叉！比较谁先出块...")

		// 【关键修复】在砍自己之前，先检查对方checkpoint是否达到已知最高
		// 如果对方checkpoint低于已知最高，说明对方不是真大哥，不跟随！
		if peerCheckpointHeight < currentKnownHighest {
			log.Printf("⚠️ 对方Checkpoint(%d)低于已知最高(%d)，对方不是真大哥，拒绝跟随！",
				peerCheckpointHeight, currentKnownHighest)
			return nil
		}

		// 比较时间戳：谁先出块（时间戳更小的）谁是大哥
		if peerCheckpointTimestamp < myCheckpointTimestamp {
			// 对方时间戳更小，说明对方先出块，认对方做大哥
			log.Printf("👑 对方先出块（%d < %d），认对方做大哥！", peerCheckpointTimestamp, myCheckpointTimestamp)
			log.Printf("🔥 【家务事】清除本地分叉链，强行跟随大哥！")
			return n.ForceResyncFromPeer(peerCheckpointHeight)
		} else if peerCheckpointTimestamp > myCheckpointTimestamp {
			// 我方时间戳更小，我方先出块，保持当前链
			log.Printf("👑 本地先出块（%d < %d），保持当前链", myCheckpointTimestamp, peerCheckpointTimestamp)
			return nil
		} else {
			// 时间戳完全相同（极端情况）- 按hash字典序决定，小的胜出
			if peerCheckpointHash < myCheckpointHash {
				log.Printf("👑 时间戳相同，对方hash更小，认对方做大哥！")
				log.Printf("🔥 【家务事】清除本地分叉链，强行跟随大哥！")
				return n.ForceResyncFromPeer(peerCheckpointHeight)
			}
			log.Printf("👑 时间戳相同，本地hash更小，保持当前链")
			return nil
		}
	}

	// 规则3：checkpoint高度相同且hash一致 → 不是分叉
	log.Printf("✓ Checkpoint完全一致（高度=%d, hash匹配），不是分叉", myCheckpointHeight)
	return nil
}

// ForceResyncFromPeer 【家务事】强行清除本地数据并从大哥那里重新同步
// 核心原则：砍一刀的深度取决于分叉点，而不是固定值
// - 如果分叉点很近（差几个区块），轻轻一刀
// - 如果分叉点很远（差很多区块），砍到分叉点
func (n *Node) ForceResyncFromPeer(peerCheckpointHeight uint64) error {
	log.Printf("🔥🔥🔥 【家务事】强制重同步开始！目标checkpoint高度: %d", peerCheckpointHeight)

	myHeight := n.chain.GetLatestHeight()

	// 计算回滚点：找到最后一个共同checkpoint或者差距最小的点
	// 原则：砍到peerCheckpoint的前一个checkpoint位置，确保能重新同步
	consensusCfg := core.GetConsensusConfig()
	interval := consensusCfg.BlockParams.CheckpointInterval

	// 计算回滚深度：砍到peer checkpoint的前一个interval
	// 这样既不会砍太多，也确保了能够重新同步
	rollbackHeight := uint64(1) // 默认回滚到创世区块

	if peerCheckpointHeight > interval {
		// 回滚到peerCheckpoint前一个interval
		rollbackHeight = peerCheckpointHeight - interval
	}

	// 优化：如果本地高度和回滚点很近，只砍到必要的深度
	// 避免不必要的深度回滚
	if myHeight > peerCheckpointHeight {
		// 本地比peer checkpoint高，只需要回滚到peer checkpoint
		rollbackHeight = peerCheckpointHeight - interval
		if rollbackHeight < 1 {
			rollbackHeight = 1
		}
		log.Printf("📊 本地高度(%d) > peer checkpoint(%d)，需要砍掉分叉部分", myHeight, peerCheckpointHeight)
	} else {
		// 本地比peer checkpoint低或相等，可能只需要轻微回滚
		// 回滚到本地高度的前一个interval，或者peer checkpoint的前一个interval，取较小值
		localRollback := uint64(1)
		if myHeight > interval {
			localRollback = myHeight - interval
		}
		peerRollback := uint64(1)
		if peerCheckpointHeight > interval {
			peerRollback = peerCheckpointHeight - interval
		}
		// 取较大的回滚点（砍得浅一点）
		if localRollback > peerRollback {
			rollbackHeight = localRollback
		} else {
			rollbackHeight = peerRollback
		}
		log.Printf("📊 本地高度(%d) <= peer checkpoint(%d)，轻微回滚即可", myHeight, peerCheckpointHeight)
	}

	log.Printf("🔄 回滚到高度 %d（本地=%d, peer checkpoint=%d, interval=%d）",
		rollbackHeight, myHeight, peerCheckpointHeight, interval)

	// 1. 删除本地checkpoint文件（单点设计，直接删除latest文件）
	checkpointDir := n.config.DataDir + "/checkpoints"
	checkpointFile := checkpointDir + "/checkpoint_latest.dat"
	stateFile := checkpointDir + "/state_latest.dat.gz"

	if err := os.Remove(checkpointFile); err != nil && !os.IsNotExist(err) {
		log.Printf("⚠️  删除checkpoint文件失败: %v", err)
	} else {
		log.Printf("✓ 已删除本地checkpoint文件")
	}

	if err := os.Remove(stateFile); err != nil && !os.IsNotExist(err) {
		log.Printf("⚠️  删除state快照文件失败: %v", err)
	} else {
		log.Printf("✓ 已删除本地state快照文件")
	}

	// 2. 删除回滚点之后的所有区块
	if err := n.db.DeleteBlocksAboveHeight(rollbackHeight); err != nil {
		log.Printf("⚠️  删除区块失败: %v，尝试继续", err)
	} else {
		log.Printf("✓ 已删除高度 %d 之后的所有区块", rollbackHeight)
	}

	// 3. 回滚链状态
	rollbackBlock, err := n.db.GetBlockByHeight(rollbackHeight)
	if err != nil {
		// 如果找不到回滚区块，尝试从高度1开始
		log.Printf("⚠️  无法找到高度 %d 的区块，尝试从高度1开始", rollbackHeight)
		rollbackHeight = 1
		rollbackBlock, err = n.db.GetBlockByHeight(1)
		if err != nil {
			log.Printf("❌ 无法找到任何有效区块，重同步失败: %v", err)
			return fmt.Errorf("无法找到有效区块进行回滚: %v", err)
		}
	}

	if err := n.chain.RollbackToHeight(rollbackHeight, rollbackBlock); err != nil {
		log.Printf("⚠️  回滚链状态失败: %v，尝试继续", err)
	} else {
		log.Printf("✓ 链状态已回滚到高度 %d", rollbackHeight)
	}

	// 4. 重新加载状态
	if err := n.state.ReloadStateFromHeight(n.db, rollbackHeight); err != nil {
		log.Printf("⚠️  重新加载状态失败: %v，尝试继续", err)
	} else {
		log.Printf("✓ 状态已重新加载")
	}

	log.Printf("✅ 【家务事】已回滚到高度 %d，准备从大哥那里同步", rollbackHeight)

	// 5. 触发重新同步（请求大范围区块，让速度跑到极限）
	if n.p2pServer != nil {
		// 请求从回滚点+1到对方checkpoint+1000的区块
		n.p2pServer.RequestSyncFromBestPeer(rollbackHeight+1, peerCheckpointHeight+1000)
	}

	return nil
}

// RollbackToCheckpoint 回滚到指定checkpoint
func (n *Node) RollbackToCheckpoint(checkpointHeight uint64) error {
	log.Printf("🔄 回滚到Checkpoint高度 %d", checkpointHeight)

	// 获取checkpoint
	_, err := n.db.LoadCheckpoint(checkpointHeight, n.config.DataDir)
	if err != nil {
		return fmt.Errorf("无法加载checkpoint: %v", err)
	}

	// 获取checkpoint对应的区块
	checkpointBlock, err := n.db.GetBlockByHeight(checkpointHeight)
	if err != nil {
		return fmt.Errorf("无法获取checkpoint区块: %v", err)
	}

	// 删除checkpoint之后的所有区块
	if err := n.db.DeleteBlocksAboveHeight(checkpointHeight); err != nil {
		return fmt.Errorf("删除区块失败: %v", err)
	}

	// 回滚链状态
	if err := n.chain.RollbackToHeight(checkpointHeight, checkpointBlock); err != nil {
		return fmt.Errorf("回滚链失败: %v", err)
	}

	// 重新加载状态（从checkpoint快照）
	if err := n.state.ReloadStateFromHeight(n.db, checkpointHeight); err != nil {
		return fmt.Errorf("重新加载状态失败: %v", err)
	}

	log.Printf("✅ 成功回滚到Checkpoint %d", checkpointHeight)

	// 验证回滚后的总供应量
	supply, correct, err := n.state.VerifyTotalSupply()
	if err != nil {
		log.Printf("⚠️  无法验证总供应量: %v", err)
	} else if !correct {
		log.Printf("🚨 警告：回滚后总供应量不正确！实际=%d", supply)
	} else {
		log.Printf("✓ 回滚后总供应量正确: %d", supply)
	}

	// 触发重新同步
	if n.p2pServer != nil {
		n.p2pServer.RequestSyncFromBestPeer(checkpointHeight+1, checkpointHeight+1000)
	}

	return nil
}
