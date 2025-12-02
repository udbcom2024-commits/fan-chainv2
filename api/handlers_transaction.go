package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"fan-chain/core"
)

// 处理交易提交
func (s *Server) handleTransaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析交易
	var tx core.Transaction
	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
		http.Error(w, fmt.Sprintf("Invalid transaction: %v", err), http.StatusBadRequest)
		return
	}

	// 验证交易
	if err := tx.VerifySignature(); err != nil {
		http.Error(w, fmt.Sprintf("Transaction verification failed: %v", err), http.StatusBadRequest)
		return
	}

	// 提交交易
	if s.submitTransaction != nil {
		if err := s.submitTransaction(&tx); err != nil {
			http.Error(w, fmt.Sprintf("Failed to submit transaction: %v", err), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "Transaction submission not available", http.StatusServiceUnavailable)
		return
	}

	// 返回成功响应
	txHash := tx.Hash()
	response := map[string]interface{}{
		"success": true,
		"message": "Transaction submitted successfully",
		"hash":    fmt.Sprintf("%x", txHash.Bytes()),
	}

	writeJSON(w, response)
	log.Printf("Transaction submitted: %x", txHash.Bytes())
}

// 按哈希查询单个交易
func (s *Server) handleTransactionByHash(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析交易哈希
	hashStr := strings.TrimPrefix(r.URL.Path, "/transaction/")
	if hashStr == "" {
		http.Error(w, "Transaction hash required", http.StatusBadRequest)
		return
	}

	// 解码哈希
	hashBytes, err := hex.DecodeString(hashStr)
	hash := core.BytesToHash(hashBytes)
	if err != nil {
		http.Error(w, "Invalid transaction hash", http.StatusBadRequest)
		return
	}

	// 查询交易
	tx, err := s.db.GetTransaction(hash)
	if err != nil {
		http.Error(w, "Transaction not found", http.StatusNotFound)
		return
	}

	// 格式化响应
	response := map[string]interface{}{
		"hash":      fmt.Sprintf("%x", tx.Hash().Bytes()),
		"type":      tx.Type,
		"from":      tx.From,
		"to":        tx.To,
		"amount":    tx.Amount,
		"gas_fee":   tx.GasFee,
		"nonce":     tx.Nonce,
		"timestamp": tx.Timestamp,
	}

	writeJSON(w, response)
}

// 处理交易历史查询
func (s *Server) handleTransactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析地址参数
	address := strings.TrimPrefix(r.URL.Path, "/transactions/")
	if address == "" {
		http.Error(w, "Address required", http.StatusBadRequest)
		return
	}

	// 验证地址格式
	if len(address) != 37 || !strings.HasPrefix(address, "F") {
		http.Error(w, "Invalid address format", http.StatusBadRequest)
		return
	}

	// 查询交易历史（默认返回最近100笔）
	transactions, err := s.db.GetTransactionsByAddress(address, 100)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to query transactions: %v", err), http.StatusInternalServerError)
		return
	}

	// 格式化交易数据
	txList := make([]map[string]interface{}, 0, len(transactions))
	for _, tx := range transactions {
		txData := map[string]interface{}{
			"hash":      fmt.Sprintf("%x", tx.Hash().Bytes()),
			"type":      tx.Type,
			"from":      tx.From,
			"to":        tx.To,
			"amount":    tx.Amount,
			"fee":       tx.GasFee,
			"timestamp": tx.Timestamp,
		}
		txList = append(txList, txData)
	}

	response := map[string]interface{}{
		"address":      address,
		"transactions": txList,
		"count":        len(txList),
	}

	writeJSON(w, response)
}

// 处理全局交易列表查询（所有类型交易，分页）
// 从最新区块倒序获取所有交易（包括Type=0转账、Type=1质押、Type=2解押、Type=3区块奖励）
func (s *Server) handleAllTransactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析分页参数
	page := 1
	limit := 20

	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		fmt.Sscanf(pageStr, "%d", &page)
		if page < 1 {
			page = 1
		}
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
		if limit < 1 || limit > 100 {
			limit = 20
		}
	}

	// 获取最新高度
	latestHeight, err := s.db.GetLatestHeight()
	if err != nil || latestHeight == 0 {
		// 没有区块，返回空列表
		response := map[string]interface{}{
			"transactions": []map[string]interface{}{},
			"total":        0,
			"page":         page,
			"limit":        limit,
			"total_pages":  1,
		}
		writeJSON(w, response)
		return
	}

	// 从最新区块倒序收集交易，直到收集够 limit 条
	txList := make([]map[string]interface{}, 0, limit)
	offset := (page - 1) * limit
	skipped := 0
	collected := 0

	// 从最新区块开始向前扫描
	for height := latestHeight; height >= 1 && collected < limit; height-- {
		block, err := s.db.GetBlockByHeight(height)
		if err != nil || block == nil {
			continue
		}

		// 倒序遍历区块内交易（最新的在前）
		for i := len(block.Transactions) - 1; i >= 0 && collected < limit; i-- {
			tx := block.Transactions[i]

			// 跳过 offset 条记录
			if skipped < offset {
				skipped++
				continue
			}

			txData := map[string]interface{}{
				"hash":         fmt.Sprintf("%x", tx.Hash().Bytes()),
				"type":         tx.Type,
				"from":         tx.From,
				"to":           tx.To,
				"amount":       tx.Amount,
				"gas_fee":      tx.GasFee,
				"block_height": height,
				"timestamp":    tx.Timestamp,
				"nonce":        tx.Nonce,
			}
			txList = append(txList, txData)
			collected++
		}
	}

	// 估算总交易数（简化：假设每个区块平均1笔交易）
	// 实际应该维护一个交易计数器，但这里简化处理
	estimatedTotal := int(latestHeight)
	totalPages := (estimatedTotal + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}

	response := map[string]interface{}{
		"transactions": txList,
		"total":        estimatedTotal,
		"page":         page,
		"limit":        limit,
		"total_pages":  totalPages,
	}

	writeJSON(w, response)
}

// 处理搜索请求（区块高度、交易哈希、地址）
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Search query required", http.StatusBadRequest)
		return
	}

	result := map[string]interface{}{
		"query": query,
		"found": false,
		"type":  "",
	}

	// 尝试解析为区块高度
	var height uint64
	if _, err := fmt.Sscanf(query, "%d", &height); err == nil {
		block, _ := s.db.GetBlockByHeight(height)
		if block != nil {
			result["found"] = true
			result["type"] = "block"
			result["height"] = height
			writeJSON(w, result)
			return
		}
	}

	// 尝试解析为地址
	if len(query) == 37 && strings.HasPrefix(query, "F") {
		account, _ := s.state.GetAccount(query)
		if account != nil {
			result["found"] = true
			result["type"] = "account"
			result["address"] = query
			writeJSON(w, result)
			return
		}
	}

	// 尝试解析为交易哈希
	if len(query) >= 10 {
		hashBytes, err := hex.DecodeString(query)
		if err == nil {
			hash := core.BytesToHash(hashBytes)
			tx, err := s.db.GetTransaction(hash)
			if err == nil && tx != nil {
				result["found"] = true
				result["type"] = "transaction"
				result["hash"] = query
				writeJSON(w, result)
				return
			}
		}
	}

	writeJSON(w, result)
}

// 处理转账列表查询（从数据库索引）
func (s *Server) handleTransfers(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析分页参数
	page := 1
	limit := 20
	address := ""

	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		fmt.Sscanf(pageStr, "%d", &page)
		if page < 1 {
			page = 1
		}
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
		if limit < 1 || limit > 100 {
			limit = 20
		}
	}
	address = r.URL.Query().Get("address")

	offset := (page - 1) * limit

	var transfers []map[string]interface{}
	var total int

	if address != "" {
		// 按地址查询
		records, count, e := s.db.GetTransfersByAddress(address, offset, limit)
		if e != nil {
			http.Error(w, fmt.Sprintf("Failed to query transfers: %v", e), http.StatusInternalServerError)
			return
		}
		total = count
		transfers = make([]map[string]interface{}, len(records))
		for i, rec := range records {
			transfers[i] = map[string]interface{}{
				"tx_hash":      rec.TxHash,
				"from":         rec.From,
				"to":           rec.To,
				"amount":       rec.Amount,
				"gas_fee":      rec.GasFee,
				"block_height": rec.BlockHeight,
				"timestamp":    rec.Timestamp,
				"nonce":        rec.Nonce,
			}
		}
	} else {
		// 查询所有转账
		records, count, e := s.db.GetTransfers(offset, limit)
		if e != nil {
			http.Error(w, fmt.Sprintf("Failed to query transfers: %v", e), http.StatusInternalServerError)
			return
		}
		total = count
		transfers = make([]map[string]interface{}, len(records))
		for i, rec := range records {
			transfers[i] = map[string]interface{}{
				"tx_hash":      rec.TxHash,
				"from":         rec.From,
				"to":           rec.To,
				"amount":       rec.Amount,
				"gas_fee":      rec.GasFee,
				"block_height": rec.BlockHeight,
				"timestamp":    rec.Timestamp,
				"nonce":        rec.Nonce,
			}
		}
	}

	totalPages := (total + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}

	response := map[string]interface{}{
		"transfers":   transfers,
		"total":       total,
		"page":        page,
		"limit":       limit,
		"total_pages": totalPages,
	}

	writeJSON(w, response)
}
