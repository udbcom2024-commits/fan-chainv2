package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"fan-chain/core"
	"fan-chain/state"
	"fan-chain/storage"
)

// API服务器
type Server struct {
	port              int
	db                *storage.Database
	state             *state.StateManager
	blockchain        *core.Blockchain
	getLatestBlock    func() *core.Block
	getPeerCount      func() int
	getAddress        func() string
	getNodeName       func() string
	submitTransaction func(*core.Transaction) error
}

// 创建API服务器
func NewServer(port int, db *storage.Database, stateManager *state.StateManager, blockchain *core.Blockchain) *Server {
	return &Server{
		port:       port,
		db:         db,
		state:      stateManager,
		blockchain: blockchain,
	}
}

// 设置回调函数
func (s *Server) SetCallbacks(
	getLatestBlock func() *core.Block,
	getPeerCount func() int,
	getAddress func() string,
	getNodeName func() string,
	submitTransaction func(*core.Transaction) error,
) {
	s.getLatestBlock = getLatestBlock
	s.getPeerCount = getPeerCount
	s.getAddress = getAddress
	s.getNodeName = getNodeName
	s.submitTransaction = submitTransaction
}

// 启动API服务器
func (s *Server) Start() error {
	// 注册路由
	http.HandleFunc("/status", s.handleStatus)
	http.HandleFunc("/stats", s.handleStats)
	http.HandleFunc("/blocks", s.handleBlocks)
	http.HandleFunc("/block/latest", s.handleLatestBlock)
	http.HandleFunc("/block/", s.handleBlock)
	http.HandleFunc("/balance/", s.handleBalance)
	http.HandleFunc("/transaction/", s.handleTransactionByHash)
	http.HandleFunc("/transaction", s.handleTransaction)
	http.HandleFunc("/transactions/", s.handleTransactions)
	http.HandleFunc("/transfers", s.handleTransfers)
	http.HandleFunc("/state/snapshot", s.handleStateSnapshot)

	addr := fmt.Sprintf("0.0.0.0:%d", s.port)
	log.Printf("API server listening on %s", addr)

	// 静态文件服务（浏览器前端）
	fs := http.FileServer(http.Dir("./web"))
	http.Handle("/", fs)

	return http.ListenAndServe(addr, nil)
}

// 写入JSON响应
func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// 格式化区块数据
func formatBlock(block *core.Block) map[string]interface{} {
	hash := block.Hash()

	// 格式化交易
	txs := make([]map[string]interface{}, len(block.Transactions))
	for i, tx := range block.Transactions {
		txHash := tx.Hash()
		txs[i] = map[string]interface{}{
			"hash":      fmt.Sprintf("%x", txHash.Bytes()),
			"type":      tx.Type,
			"from":      tx.From,
			"to":        tx.To,
			"amount":    tx.Amount,
			"gas_fee":   tx.GasFee,
			"nonce":     tx.Nonce,
			"timestamp": tx.Timestamp,
		}
	}

	return map[string]interface{}{
		"height":        block.Header.Height,
		"hash":          fmt.Sprintf("%x", hash.Bytes()),
		"previous_hash": fmt.Sprintf("%x", block.Header.PreviousHash.Bytes()),
		"timestamp":     block.Header.Timestamp,
		"proposer":      block.Header.Proposer,
		"tx_count":      len(block.Transactions),
		"transactions":  txs,
	}
}
