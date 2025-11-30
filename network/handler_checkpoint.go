package network

import (
	"log"
)

// 处理获取checkpoint请求
func (s *Server) handleGetCheckpoint(peer *Peer, msg *Message) {
	var req GetCheckpointMessage
	if err := msg.ParsePayload(&req); err != nil {
		log.Printf("Failed to parse get checkpoint from %s: %v", peer.host, err)
		return
	}

	// 默认请求3个checkpoint
	count := int(req.Count)
	if count == 0 {
		count = 3
	}

	log.Printf("Peer %s requesting latest %d checkpoints", peer.host, count)

	// 调用回调获取最新N个checkpoint
	if s.getLatestCheckpoints == nil {
		log.Printf("getLatestCheckpoints not configured")
		return
	}

	checkpoints := s.getLatestCheckpoints(count)
	if len(checkpoints) == 0 {
		log.Printf("No checkpoints available")
		return
	}

	// 发送checkpoint元数据列表
	checkpointMsg, err := NewMessage(MsgCheckpoint, &CheckpointMessage{
		Checkpoints: checkpoints,
	})
	if err != nil {
		log.Printf("Failed to create checkpoint message: %v", err)
		return
	}

	peer.SendMessage(checkpointMsg)
	log.Printf("Sent %d checkpoints to %s (heights: ", len(checkpoints), peer.host)
	for i, cp := range checkpoints {
		if i > 0 {
			log.Printf(", ")
		}
		log.Printf("%d", cp.Checkpoint.Height)
	}
	log.Printf(")")
}

// 处理checkpoint数据（现在是多个checkpoint）
func (s *Server) handleCheckpoint(peer *Peer, msg *Message) {
	var checkpointMsg CheckpointMessage
	if err := msg.ParsePayload(&checkpointMsg); err != nil {
		log.Printf("Failed to parse checkpoint from %s: %v", peer.host, err)
		return
	}

	if len(checkpointMsg.Checkpoints) == 0 {
		log.Printf("Received empty checkpoint list from %s", peer.host)
		return
	}

	log.Printf("📌 Received %d checkpoints from %s", len(checkpointMsg.Checkpoints), peer.host)

	// 只应用最新的checkpoint（第一个）
	latestCheckpointInfo := checkpointMsg.Checkpoints[0]
	log.Printf("Applying latest checkpoint at height %d (has_state: %v, size: %d bytes)",
		latestCheckpointInfo.Checkpoint.Height,
		latestCheckpointInfo.HasStateData,
		latestCheckpointInfo.CompressedSize)

	// 调用回调应用checkpoint
	if s.applyCheckpoint != nil {
		if err := s.applyCheckpoint(latestCheckpointInfo.Checkpoint); err != nil {
			log.Printf("Failed to apply checkpoint: %v", err)
			return
		}
		log.Printf("✅ Checkpoint applied at height %d", latestCheckpointInfo.Checkpoint.Height)

		// 如果有状态数据，请求状态快照
		if latestCheckpointInfo.HasStateData {
			log.Printf("Requesting state snapshot for height %d", latestCheckpointInfo.Checkpoint.Height)
			stateReq := &GetStateMessage{Height: latestCheckpointInfo.Checkpoint.Height}
			stateReqMsg, err := NewMessage(MsgGetState, stateReq)
			if err != nil {
				log.Printf("Failed to create get state message: %v", err)
				return
			}
			peer.SendMessage(stateReqMsg)
		}
	}
}

// 处理获取状态快照请求
func (s *Server) handleGetState(peer *Peer, msg *Message) {
	var req GetStateMessage
	if err := msg.ParsePayload(&req); err != nil {
		log.Printf("Failed to parse get state from %s: %v", peer.host, err)
		return
	}

	log.Printf("Peer %s requesting state snapshot at height %d", peer.host, req.Height)

	// 调用回调获取状态快照
	if s.getStateSnapshot == nil {
		log.Printf("getStateSnapshot not configured")
		return
	}

	compressedData, err := s.getStateSnapshot(req.Height)
	if err != nil {
		log.Printf("Failed to get state snapshot: %v", err)
		return
	}

	// 发送状态数据
	stateMsg, err := NewMessage(MsgStateData, &StateDataMessage{
		Height:         req.Height,
		CompressedData: compressedData,
	})
	if err != nil {
		log.Printf("Failed to create state data message: %v", err)
		return
	}

	peer.SendMessage(stateMsg)
	log.Printf("Sent state snapshot at height %d to %s (%d bytes)", req.Height, peer.host, len(compressedData))
}

// 处理状态快照数据
func (s *Server) handleStateData(peer *Peer, msg *Message) {
	var stateMsg StateDataMessage
	if err := msg.ParsePayload(&stateMsg); err != nil {
		log.Printf("Failed to parse state data from %s: %v", peer.host, err)
		return
	}

	log.Printf("📦 Received state snapshot at height %d from %s (%d bytes)",
		stateMsg.Height, peer.host, len(stateMsg.CompressedData))

	// 调用回调应用状态快照
	if s.applyStateSnapshot != nil {
		if err := s.applyStateSnapshot(stateMsg.Height, stateMsg.CompressedData); err != nil {
			log.Printf("Failed to apply state snapshot: %v", err)
			return
		}
		log.Printf("✅ State snapshot applied at height %d", stateMsg.Height)

		// 状态快照应用成功后，从checkpoint前一个周期开始同步
		if s.getLatestBlock != nil {
			// 从checkpoint高度往前推12个区块开始同步
			syncFromHeight := stateMsg.Height
			if syncFromHeight > 12 {
				syncFromHeight = syncFromHeight - 12
			}

			// 只在checkpointSyncFrom未设置时才初始化，避免覆盖backfill更新的值
			s.syncMu.Lock()
			if s.checkpointSyncFrom == 0 {
				s.checkpointSyncFrom = syncFromHeight
				log.Printf("📡 Will sync blocks from height %d (checkpoint - 12)",
					syncFromHeight)
			} else {
				log.Printf("📡 Using existing sync start height %d (already in progress)",
					s.checkpointSyncFrom)
			}
			s.checkpointHeight = stateMsg.Height
			s.syncMu.Unlock()

			// 先询问对方的最新高度
			latestMsg, err := NewMessage(MsgGetLatest, nil)
			if err == nil {
				peer.SendMessage(latestMsg)
				log.Printf("📡 Requesting latest height from peer to sync blocks after checkpoint %d", stateMsg.Height)
			}

			// 【P2协议-缓冲期修复】只有在backfill未进行时才启动新的backfill
			// 防止每次收到新checkpoint都重复触发backfill请求
			s.syncMu.Lock()
			backfillInProgress := s.backfillInProgress
			s.syncMu.Unlock()

			if !backfillInProgress {
				// 【P2协议】询问大哥的最早区块高度，启动向下同步
				log.Printf("📡 【P2】Requesting big brother's earliest block height for backfill sync...")
				earliestMsg, err := NewMessage(MsgGetEarliestHeight, &GetEarliestHeightMessage{})
				if err == nil {
					peer.SendMessage(earliestMsg)
				}
			} else {
				log.Printf("📡 【P2】Backfill already in progress, skipping new backfill request")
			}
		}
	}
}
