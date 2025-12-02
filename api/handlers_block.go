package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// 处理最新区块查询
func (s *Server) handleLatestBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 【修复】从数据库获取真正保存的最新区块，而不是内存中的虚拟区块
	block, err := s.db.GetLatestBlock()
	if err != nil || block == nil {
		http.Error(w, "No blocks found", http.StatusNotFound)
		return
	}

	writeJSON(w, formatBlock(block))
}

// 处理区块查询
func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析高度参数
	path := strings.TrimPrefix(r.URL.Path, "/block/")
	height, err := strconv.ParseUint(path, 10, 64)
	if err != nil {
		http.Error(w, "Invalid height", http.StatusBadRequest)
		return
	}

	// 从数据库查询区块
	block, err := s.db.GetBlockByHeight(height)
	if err != nil {
		http.Error(w, "Block not found", http.StatusNotFound)
		return
	}

	writeJSON(w, formatBlock(block))
}

// 区块列表分页查询
func (s *Server) handleBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析分页参数
	query := r.URL.Query()
	page := 1
	limit := 20

	// 支持page参数（前端使用）
	if pageStr := query.Get("page"); pageStr != "" {
		if val, err := strconv.Atoi(pageStr); err == nil && val > 0 {
			page = val
		}
	}

	if limitStr := query.Get("limit"); limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	// 将page转换为offset
	offset := (page - 1) * limit

	// 【修复】使用数据库中已保存的最新高度，而不是内存中的虚拟高度
	currentHeight, _ := s.db.GetLatestHeight()

	// 计算查询范围
	if offset < 0 {
		offset = 0
	}

	startHeight := uint64(0)
	if currentHeight > uint64(offset) {
		startHeight = currentHeight - uint64(offset)
	}

	// 查询区块
	blocks := make([]map[string]interface{}, 0, limit)
	for i := 0; i < limit && startHeight > 0; i++ {
		block, err := s.db.GetBlockByHeight(startHeight)
		if err != nil {
			break
		}

		blocks = append(blocks, map[string]interface{}{
			"height":        block.Header.Height,
			"hash":          fmt.Sprintf("%x", block.Hash().Bytes()),
			"previous_hash": fmt.Sprintf("%x", block.Header.PreviousHash.Bytes()),
			"timestamp":     block.Header.Timestamp,
			"proposer":      block.Header.Proposer,
			"tx_count":      len(block.Transactions),
		})

		startHeight--
	}

	// 计算总页数
	total := int(currentHeight)
	totalPages := (total + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}

	response := map[string]interface{}{
		"blocks":      blocks,
		"total":       total,
		"page":        page,
		"limit":       limit,
		"total_pages": totalPages,
	}

	writeJSON(w, response)
}
