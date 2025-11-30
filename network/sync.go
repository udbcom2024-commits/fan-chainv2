package network

import (
	"fmt"
	"log"
	"time"

	"fan-chain/core"
)

// 请求同步区块
func (s *Server) requestSync(peer *Peer, fromHeight, toHeight uint64) {
	s.syncMu.Lock()
	if s.syncing {
		s.syncMu.Unlock()
		return // 已经在同步中
	}
	s.syncing = true
	s.syncTargetHeight = toHeight
	s.syncPeer = peer
	s.syncMu.Unlock()

	// 开始分批同步
	s.requestNextBatch(peer, fromHeight)
}

// 请求下一批区块（从共识参数读取批次大小）
func (s *Server) requestNextBatch(peer *Peer, fromHeight uint64) {
	batchSize := uint64(core.GetConsensusConfig().NetworkParams.SyncBatchSize)

	s.syncMu.Lock()
	targetHeight := s.syncTargetHeight
	s.syncMu.Unlock()

	// 计算本批次的结束高度
	batchEnd := fromHeight + batchSize - 1
	if batchEnd > targetHeight {
		batchEnd = targetHeight
	}

	log.Printf("Requesting batch: blocks %d-%d (target: %d)", fromHeight, batchEnd, targetHeight)

	// 发送GetBlocks请求
	req := &GetBlocksMessage{
		FromHeight: fromHeight,
		ToHeight:   batchEnd,
	}

	reqMsg, err := NewMessage(MsgGetBlocks, req)
	if err != nil {
		log.Printf("Failed to create get blocks message: %v", err)
		s.finishSync()
		return
	}

	if err := peer.SendMessage(reqMsg); err != nil {
		log.Printf("Failed to send get blocks request: %v", err)
		s.finishSync()
		return
	}

	log.Printf("Batch request sent to %s for blocks %d-%d", peer.host, fromHeight, batchEnd)
}

// 完成同步
func (s *Server) finishSync() {
	s.syncMu.Lock()
	s.syncing = false
	s.syncTargetHeight = 0
	s.syncPeer = nil
	s.syncStartTime = time.Time{}
	s.lastSyncHeight = 0
	s.progressiveSkip = 0
	s.syncMu.Unlock()
	log.Println("Sync finished")
}

// 检查是否正在同步
func (s *Server) IsSyncing() bool {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return s.syncing
}

// 获取同步目标高度
func (s *Server) GetSyncTargetHeight() uint64 {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return s.syncTargetHeight
}

// RequestSyncFromBestPeer 主动从任意peer请求同步（公开方法，供外部调用）
func (s *Server) RequestSyncFromBestPeer(fromHeight, toHeight uint64) {
	s.peersMu.RLock()
	defer s.peersMu.RUnlock()

	// 选择第一个可用的peer
	var selectedPeer *Peer
	for _, peer := range s.peers {
		if peer.IsConnected() {
			selectedPeer = peer
			break
		}
	}

	if selectedPeer == nil {
		log.Printf("No peers available for sync request")
		return
	}

	log.Printf("Proactive sync: requesting blocks %d-%d from peer %s",
		fromHeight, toHeight, truncateAddr(selectedPeer.address))

	// 调用私有的requestSync方法
	s.requestSync(selectedPeer, fromHeight, toHeight)
}

// RequestBlocksDirect 直接请求区块，不经过sync状态机（用于checkpoint同步等特殊场景）
func (s *Server) RequestBlocksDirect(fromHeight, toHeight uint64) error {
	s.peersMu.RLock()
	defer s.peersMu.RUnlock()

	// 选择第一个可用的peer
	var selectedPeer *Peer
	for _, peer := range s.peers {
		if peer.IsConnected() {
			selectedPeer = peer
			break
		}
	}

	if selectedPeer == nil {
		return fmt.Errorf("no peers available for direct block request")
	}

	log.Printf("Direct block request: %d-%d from peer %s (bypassing sync state)",
		fromHeight, toHeight, truncateAddr(selectedPeer.address))

	// 直接发送GetBlocks请求，不修改sync状态
	req := &GetBlocksMessage{
		FromHeight: fromHeight,
		ToHeight:   toHeight,
	}

	reqMsg, err := NewMessage(MsgGetBlocks, req)
	if err != nil {
		return fmt.Errorf("failed to create get blocks message: %v", err)
	}

	if err := selectedPeer.SendMessage(reqMsg); err != nil {
		return fmt.Errorf("failed to send direct block request: %v", err)
	}

	log.Printf("Direct block request sent to %s for blocks %d-%d", selectedPeer.host, fromHeight, toHeight)
	return nil
}

// truncateAddr 安全截取地址前10个字符（防止空地址panic）
func truncateAddr(addr string) string {
	if len(addr) == 0 {
		return "(empty)"
	}
	if len(addr) <= 10 {
		return addr
	}
	return addr[:10]
}

// 检查同步超时并触发渐进式快速同步
func (s *Server) checkSyncTimeout() {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	if !s.syncing {
		return
	}

	// 检查是否超过10秒未同步进度
	elapsed := time.Since(s.syncStartTime)
	if elapsed < 10*time.Second {
		return
	}

	// 获取当前高度
	var currentHeight uint64
	if s.getLatestBlock != nil {
		latestBlock := s.getLatestBlock()
		if latestBlock != nil {
			currentHeight = latestBlock.Header.Height
		}
	}

	// 检查是否有进度
	if currentHeight > s.lastSyncHeight {
		// 有进度，重置计时器
		s.syncStartTime = time.Now()
		s.lastSyncHeight = currentHeight
		s.progressiveSkip = 0
		return
	}

	// 10秒无进度，触发渐进式快速同步
	targetHeight := s.syncTargetHeight
	syncPeer := s.syncPeer

	// 计算新的跳过数量：0 → 1000 → 10000 → 100000
	if s.progressiveSkip == 0 {
		s.progressiveSkip = 1000
	} else if s.progressiveSkip == 1000 {
		s.progressiveSkip = 10000
	} else if s.progressiveSkip == 10000 {
		s.progressiveSkip = 100000
	} else {
		// 已经是最大跳跃，放弃同步
		log.Printf("⚠️ SYNC TIMEOUT: Failed to sync after multiple attempts, giving up")
		s.syncing = false
		return
	}

	// 计算新的同步起点
	newSyncFrom := currentHeight + s.progressiveSkip
	if newSyncFrom >= targetHeight {
		newSyncFrom = targetHeight - 10 // 至少同步最后10个区块
		if newSyncFrom <= currentHeight {
			newSyncFrom = currentHeight + 1
		}
	}

	log.Printf("⚡ PROGRESSIVE FAST SYNC: No progress for 10s, skipping %d blocks (from height %d to %d, target: %d)",
		s.progressiveSkip, currentHeight, newSyncFrom, targetHeight)

	// 重置计时器
	s.syncStartTime = time.Now()
	s.lastSyncHeight = currentHeight

	// 发送新的同步请求（需要先解锁）
	s.syncMu.Unlock()
	s.requestNextBatch(syncPeer, newSyncFrom)
	s.syncMu.Lock()
}
