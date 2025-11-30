package api

import (
	"net/http"
)

// 处理状态查询
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 【修复】使用数据库中已保存的最新高度，而不是内存中的虚拟高度
	// 这样在checkpoint同步过程中，显示的是真实已保存的区块高度
	var height uint64
	var peerCount int
	var address string
	var nodeName string

	// 从数据库获取已保存的最新高度
	if dbHeight, err := s.db.GetLatestHeight(); err == nil {
		height = dbHeight
	}

	if s.getPeerCount != nil {
		peerCount = s.getPeerCount()
	}

	if s.getAddress != nil {
		address = s.getAddress()
	}

	if s.getNodeName != nil {
		nodeName = s.getNodeName()
	}

	response := map[string]interface{}{
		"height":    height,
		"peers":     peerCount,
		"address":   address,
		"node_name": nodeName,
		"running":   true,
	}

	writeJSON(w, response)
}

// 链上统计信息
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var peerCount int = 0

	// 【修复】使用数据库中已保存的最新高度
	height, _ := s.db.GetLatestHeight()

	if s.getPeerCount != nil {
		peerCount = s.getPeerCount()
	}

	// 统计总交易数（遍历所有区块）
	totalTx := uint64(0)
	for i := uint64(1); i <= height; i++ {
		block, err := s.db.GetBlockByHeight(i)
		if err != nil {
			continue
		}
		totalTx += uint64(len(block.Transactions))
	}

	response := map[string]interface{}{
		"block_height":       height,
		"total_transactions": totalTx,
		"peer_count":         peerCount,
		"network":            "FAN Chain",
	}

	writeJSON(w, response)
}
