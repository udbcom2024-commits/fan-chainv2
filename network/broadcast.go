package network

import (
	"log"
	"time"

	"fan-chain/core"
)

// 广播新区块
func (s *Server) BroadcastBlock(block *core.Block) {
	msg, err := NewMessage(MsgNewBlock, &NewBlockMessage{Block: block})
	if err != nil {
		log.Printf("Failed to create new block message: %v", err)
		return
	}

	s.peersMu.RLock()
	peerCount := len(s.peers)
	for _, peer := range s.peers {
		if peer.IsConnected() {
			go peer.SendMessage(msg)
		}
	}
	s.peersMu.RUnlock()

	// 记录广播信息用于重试机制
	s.broadcastMu.Lock()
	s.lastBroadcastHeight = block.Header.Height
	s.lastBroadcastTime = time.Now()
	s.broadcastMu.Unlock()

	log.Printf("Broadcasted block #%d to %d peers", block.Header.Height, peerCount)
}

// 监控区块高度并在停滞时重复广播
// 理论出块速度5-5.5秒，若高度停滞6秒，立即重复广播最新区块
func (s *Server) monitorAndRetryBroadcast() {
	ticker := time.NewTicker(2 * time.Second) // 每2秒检查一次
	defer ticker.Stop()

	for {
		select {
		case <-s.closeChan:
			return
		case <-ticker.C:
			s.checkAndRetryBroadcast()
		}
	}
}

// 检查是否需要重试广播
func (s *Server) checkAndRetryBroadcast() {
	// 获取当前区块高度
	var currentHeight uint64
	if s.getLatestBlock != nil {
		latestBlock := s.getLatestBlock()
		if latestBlock != nil {
			currentHeight = latestBlock.Header.Height
		}
	}

	if currentHeight == 0 {
		return // 还没有区块，不需要广播
	}

	s.broadcastMu.Lock()
	lastHeight := s.lastBroadcastHeight
	lastTime := s.lastBroadcastTime
	s.broadcastMu.Unlock()

	// 检查是否有新区块产生（高度增加）
	if currentHeight > lastHeight {
		// 高度增加了，重置计时器（更新会在下次BroadcastBlock时自动更新）
		return
	}

	// 检查是否已经广播过
	if lastHeight == 0 || lastTime.IsZero() {
		return
	}

	// 检查时间是否超过配置的重试间隔（从共识参数读取）
	retryInterval := time.Duration(core.GetConsensusConfig().NetworkParams.BroadcastRetryInterval) * time.Second
	elapsed := time.Since(lastTime)
	if elapsed >= retryInterval {
		// 高度停滞超过重试间隔，重新广播最新区块
		if s.getLatestBlock != nil {
			latestBlock := s.getLatestBlock()
			if latestBlock != nil && latestBlock.Header.Height == currentHeight {
				log.Printf("⚠️  Height stalled at #%d for %.1fs, re-broadcasting block", currentHeight, elapsed.Seconds())

				// 重新广播（不更新lastBroadcastTime，以便下次继续重试直到高度增加）
				msg, err := NewMessage(MsgNewBlock, &NewBlockMessage{Block: latestBlock})
				if err != nil {
					log.Printf("Failed to create retry broadcast message: %v", err)
					return
				}

				s.peersMu.RLock()
				peerCount := 0
				for _, peer := range s.peers {
					if peer.IsConnected() {
						go peer.SendMessage(msg)
						peerCount++
					}
				}
				s.peersMu.RUnlock()

				log.Printf("🔄 Re-broadcasted block #%d to %d peers", currentHeight, peerCount)

				// 更新重试时间，避免过于频繁重试（每retryInterval最多重试一次）
				s.broadcastMu.Lock()
				s.lastBroadcastTime = time.Now()
				s.broadcastMu.Unlock()
			}
		}
	}
}

// 广播checkpoint（包含状态快照）到所有节点
func (s *Server) BroadcastCheckpoint(checkpoint *core.Checkpoint, compressedSnapshot []byte) {
	// 构建checkpoint信息
	checkpointInfo := CheckpointInfo{
		Checkpoint:     checkpoint,
		HasStateData:   len(compressedSnapshot) > 0,
		CompressedSize: uint64(len(compressedSnapshot)),
	}

	// 发送checkpoint消息
	msg, err := NewMessage(MsgCheckpoint, &CheckpointMessage{
		Checkpoints: []CheckpointInfo{checkpointInfo},
	})
	if err != nil {
		log.Printf("Failed to create checkpoint broadcast message: %v", err)
		return
	}

	s.peersMu.RLock()
	peerCount := 0
	for _, peer := range s.peers {
		if peer.IsConnected() {
			go func(p *Peer) {
				// 先发送checkpoint
				p.SendMessage(msg)
				// 如果有状态快照，紧接着发送
				if len(compressedSnapshot) > 0 {
					stateMsg, err := NewMessage(MsgStateData, &StateDataMessage{
						Height:         checkpoint.Height,
						CompressedData: compressedSnapshot,
					})
					if err == nil {
						p.SendMessage(stateMsg)
					}
				}
			}(peer)
			peerCount++
		}
	}
	s.peersMu.RUnlock()

	log.Printf("📡 Broadcasted checkpoint #%d with state snapshot (%d bytes) to %d peers",
		checkpoint.Height, len(compressedSnapshot), peerCount)
}

// 发送心跳到所有节点
func (s *Server) sendHeartbeatToAll() {
	s.peersMu.RLock()
	peers := make([]*Peer, 0, len(s.peers))
	for _, peer := range s.peers {
		if peer.IsConnected() {
			peers = append(peers, peer)
		}
	}
	s.peersMu.RUnlock()

	for _, peer := range peers {
		s.sendPing(peer)
	}
}
