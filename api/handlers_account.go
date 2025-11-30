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
