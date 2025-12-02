package api

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"fan-chain/core"
)

// 处理余额查询
func (s *Server) handleBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析地址参数
	address := strings.TrimPrefix(r.URL.Path, "/balance/")
	if address == "" {
		http.Error(w, "Address required", http.StatusBadRequest)
		return
	}

	// 验证地址格式
	if !core.ValidateAddress(address) {
		http.Error(w, "Invalid address format", http.StatusBadRequest)
		return
	}

	// 查询余额
	account, _ := s.state.GetAccount(address)
	totalBalance := uint64(0)
	availableBalance := uint64(0)
	stakedBalance := uint64(0)
	nonce := uint64(0)

	if account != nil {
		availableBalance = account.AvailableBalance
		stakedBalance = account.StakedBalance
		totalBalance = availableBalance + stakedBalance
		nonce = account.Nonce
	}

	response := map[string]interface{}{
		"address":           address,
		"available_balance": availableBalance,
		"staked_balance":    stakedBalance,
		"total_balance":     totalBalance,
		"nonce":             nonce,
	}

	writeJSON(w, response)
}

// 处理账户列表查询（按余额排序）
func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
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

	// 获取所有账户
	accounts, err := s.db.GetAllAccounts()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get accounts: %v", err), http.StatusInternalServerError)
		return
	}

	// 按总余额排序（降序）
	type accountWithBalance struct {
		Address          string `json:"address"`
		AvailableBalance uint64 `json:"available_balance"`
		StakedBalance    uint64 `json:"staked_balance"`
		TotalBalance     uint64 `json:"total_balance"`
		Nonce            uint64 `json:"nonce"`
	}

	sorted := make([]accountWithBalance, 0, len(accounts))
	for _, acc := range accounts {
		total := acc.AvailableBalance + acc.StakedBalance
		sorted = append(sorted, accountWithBalance{
			Address:          acc.Address,
			AvailableBalance: acc.AvailableBalance,
			StakedBalance:    acc.StakedBalance,
			TotalBalance:     total,
			Nonce:            acc.Nonce,
		})
	}

	// 按总余额排序（简单冒泡排序）
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].TotalBalance > sorted[i].TotalBalance {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// 分页
	total := len(sorted)
	offset := (page - 1) * limit
	end := offset + limit
	if offset > total {
		offset = total
	}
	if end > total {
		end = total
	}

	paged := sorted[offset:end]
	totalPages := (total + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}

	response := map[string]interface{}{
		"accounts":    paged,
		"total":       total,
		"page":        page,
		"limit":       limit,
		"total_pages": totalPages,
	}

	writeJSON(w, response)
}

// 处理账户详情查询（包括交易历史）
func (s *Server) handleAccountDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析路径：/account/{address} 或 /account/{address}/transactions
	path := strings.TrimPrefix(r.URL.Path, "/account/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Address required", http.StatusBadRequest)
		return
	}

	address := parts[0]

	// 验证地址格式
	if !core.ValidateAddress(address) {
		http.Error(w, "Invalid address format", http.StatusBadRequest)
		return
	}

	// 如果是 /account/{address}/transactions 请求
	if len(parts) >= 2 && parts[1] == "transactions" {
		s.handleAccountTransactions(w, r, address)
		return
	}

	// 返回账户详情
	account, _ := s.state.GetAccount(address)
	totalBalance := uint64(0)
	availableBalance := uint64(0)
	stakedBalance := uint64(0)
	nonce := uint64(0)

	if account != nil {
		availableBalance = account.AvailableBalance
		stakedBalance = account.StakedBalance
		totalBalance = availableBalance + stakedBalance
		nonce = account.Nonce
	}

	response := map[string]interface{}{
		"address":           address,
		"available_balance": availableBalance,
		"staked_balance":    stakedBalance,
		"total_balance":     totalBalance,
		"nonce":             nonce,
	}

	writeJSON(w, response)
}

// 处理账户交易历史查询
func (s *Server) handleAccountTransactions(w http.ResponseWriter, r *http.Request, address string) {
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

	// 查询转账记录
	records, total, err := s.db.GetTransfersByAddress(address, (page-1)*limit, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to query transactions: %v", err), http.StatusInternalServerError)
		return
	}

	// 格式化数据
	transactions := make([]map[string]interface{}, len(records))
	for i, rec := range records {
		transactions[i] = map[string]interface{}{
			"hash":         rec.TxHash,
			"type":         1, // 转账类型
			"from":         rec.From,
			"to":           rec.To,
			"amount":       rec.Amount,
			"gas_fee":      rec.GasFee,
			"block_height": rec.BlockHeight,
			"timestamp":    rec.Timestamp,
		}
	}

	totalPages := (total + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}

	response := map[string]interface{}{
		"address":      address,
		"transactions": transactions,
		"total":        total,
		"page":         page,
		"limit":        limit,
		"total_pages":  totalPages,
	}

	writeJSON(w, response)
}

// 处理状态快照查询
func (s *Server) handleStateSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 获取所有账户
	accounts, err := s.db.GetAllAccounts()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get accounts: %v", err), http.StatusInternalServerError)
		return
	}

	// 格式化账户数据
	accountList := make([]map[string]interface{}, 0, len(accounts))
	for _, acc := range accounts {
		accountList = append(accountList, map[string]interface{}{
			"address":           acc.Address,
			"available_balance": acc.AvailableBalance,
			"staked_balance":    acc.StakedBalance,
			"nonce":             acc.Nonce,
			"node_type":         acc.NodeType,
		})
	}

	// 获取当前高度
	var height uint64
	if s.getLatestBlock != nil {
		if block := s.getLatestBlock(); block != nil {
			height = block.Header.Height
		}
	}

	response := map[string]interface{}{
		"height":   height,
		"accounts": accountList,
		"count":    len(accountList),
	}

	writeJSON(w, response)
	log.Printf("State snapshot requested: %d accounts at height %d", len(accountList), height)
}
