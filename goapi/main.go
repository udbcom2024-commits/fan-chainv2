package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	NODE_API    = "http://localhost:9000"
	SERVER_PORT = "8080"
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	clients      = make(map[*websocket.Conn]bool)
	clientsMutex sync.Mutex
	broadcast    = make(chan WSMessage, 100)

	// 转账交易缓存（仅保存Type=0的交易）
	transfersCache      = []interface{}{}
	transfersCacheMutex sync.RWMutex
	maxTransfersCache   = 1000 // 缓存最新1000条转账
)

type WSMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// CORS中间件
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

// 代理请求到节点API
func proxyToNode(endpoint string) ([]byte, error) {
	url := NODE_API + endpoint
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// WebSocket处理
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket升级失败:", err)
		return
	}

	clientsMutex.Lock()
	clients[conn] = true
	clientsMutex.Unlock()

	log.Printf("新的WebSocket连接，当前连接数: %d", len(clients))

	defer func() {
		clientsMutex.Lock()
		delete(clients, conn)
		clientsMutex.Unlock()
		conn.Close()
		log.Printf("WebSocket连接关闭，当前连接数: %d", len(clients))
	}()

	// 保持连接
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// 广播消息给所有客户端
func handleBroadcast() {
	for {
		msg := <-broadcast

		clientsMutex.Lock()
		for client := range clients {
			err := client.WriteJSON(msg)
			if err != nil {
				log.Println("发送消息失败:", err)
				client.Close()
				delete(clients, client)
			}
		}
		clientsMutex.Unlock()
	}
}

// 初始化转账缓存（从历史区块加载）
func initTransfersCache() {
	log.Println("初始化转账缓存...")

	statusData, err := proxyToNode("/status")
	if err != nil {
		log.Println("获取状态失败，跳过缓存初始化")
		return
	}

	var status map[string]interface{}
	json.Unmarshal(statusData, &status)
	currentHeight := int(status["height"].(float64))

	log.Printf("开始扫描区块... 当前高度: %d", currentHeight)

	// 从最新区块向前扫描，直到收集到足够的转账
	tempTransfers := []interface{}{}
	batchSize := 100 // 每批扫描100个区块

	for startHeight := currentHeight; startHeight >= 1 && len(tempTransfers) < maxTransfersCache; startHeight -= batchSize {
		endHeight := startHeight - batchSize + 1
		if endHeight < 1 {
			endHeight = 1
		}

		// 批量扫描这一批区块
		for h := startHeight; h >= endHeight && len(tempTransfers) < maxTransfersCache; h-- {
			blockData, err := proxyToNode(fmt.Sprintf("/block/%d", h))
			if err != nil {
				continue
			}

			var block map[string]interface{}
			json.Unmarshal(blockData, &block)

			if txs, ok := block["transactions"].([]interface{}); ok {
				for _, tx := range txs {
					if txMap, ok := tx.(map[string]interface{}); ok {
						if txType, ok := txMap["type"].(float64); ok && int(txType) == 0 {
							tempTransfers = append(tempTransfers, tx)
							if len(tempTransfers) >= maxTransfersCache {
								break
							}
						}
					}
				}
			}
		}

		// 每批输出一次进度
		if len(tempTransfers) > 0 || startHeight%1000 == 0 {
			log.Printf("扫描进度: 高度 %d, 已找到 %d 条转账", startHeight, len(tempTransfers))
		}
	}

	// 更新缓存
	transfersCacheMutex.Lock()
	transfersCache = tempTransfers
	transfersCacheMutex.Unlock()

	log.Printf("转账缓存初始化完成: 缓存 %d 条转账", len(tempTransfers))
}

// 监控区块链变化并推送
func monitorBlockchain() {
	var lastHeight int

	// 获取初始高度
	statusData, err := proxyToNode("/status")
	if err == nil {
		var status map[string]interface{}
		json.Unmarshal(statusData, &status)
		if height, ok := status["height"].(float64); ok {
			lastHeight = int(height)
			log.Printf("区块链监控初始化完成，当前高度: %d", lastHeight)
		}
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		statusData, err := proxyToNode("/status")
		if err != nil {
			continue
		}

		var status map[string]interface{}
		json.Unmarshal(statusData, &status)

		currentHeight := int(status["height"].(float64))

		if currentHeight > lastHeight {
			// 新区块产生
			log.Printf("🔔 检测到新区块: %d -> %d", lastHeight, currentHeight)

			for h := lastHeight + 1; h <= currentHeight; h++ {
				blockData, err := proxyToNode(fmt.Sprintf("/block/%d", h))
				if err != nil {
					continue
				}

				var block map[string]interface{}
				json.Unmarshal(blockData, &block)

				// 推送新区块
				broadcast <- WSMessage{
					Type:    "new_block",
					Payload: block,
				}
				log.Printf("  📤 推送新区块 #%d 到 %d 个客户端", h, len(clients))

				// 推送区块中的交易，并更新转账缓存
				if txs, ok := block["transactions"].([]interface{}); ok {
					for _, tx := range txs {
						broadcast <- WSMessage{
							Type:    "new_transaction",
							Payload: tx,
						}

						// 检查是否是转账交易（Type=0），添加到缓存
						if txMap, ok := tx.(map[string]interface{}); ok {
							if txType, ok := txMap["type"].(float64); ok && int(txType) == 0 {
								transfersCacheMutex.Lock()
								// 在前面插入新交易（最新的在前）
								transfersCache = append([]interface{}{tx}, transfersCache...)
								// 限制缓存大小
								if len(transfersCache) > maxTransfersCache {
									transfersCache = transfersCache[:maxTransfersCache]
								}
								transfersCacheMutex.Unlock()
								log.Printf("  💰 缓存转账交易，当前缓存数: %d", len(transfersCache))
							}
						}
					}
					if len(txs) > 0 {
						log.Printf("  📤 推送 %d 个交易到客户端", len(txs))
					}
				}
			}

			lastHeight = currentHeight

			// 推送状态更新
			broadcast <- WSMessage{
				Type:    "status_update",
				Payload: status,
			}
		}
	}
}

// GET /status - 节点状态
func handleStatus(w http.ResponseWriter, r *http.Request) {
	data, err := proxyToNode("/status")
	if err != nil {
		http.Error(w, "Failed to get status", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// GET /blocks?page=1&limit=20 - 区块列表
func handleBlocks(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	// 获取当前高度
	statusData, err := proxyToNode("/status")
	if err != nil {
		http.Error(w, "Failed to get status", http.StatusInternalServerError)
		return
	}

	var status map[string]interface{}
	json.Unmarshal(statusData, &status)
	currentHeight := int(status["height"].(float64))

	// 计算起始区块
	startHeight := currentHeight - (page-1)*limit
	endHeight := startHeight - limit + 1
	if endHeight < 1 {
		endHeight = 1
	}

	// 获取区块数据
	blocks := []interface{}{}
	for h := startHeight; h >= endHeight && h >= 1; h-- {
		blockData, err := proxyToNode(fmt.Sprintf("/block/%d", h))
		if err != nil {
			continue
		}

		var block map[string]interface{}
		json.Unmarshal(blockData, &block)
		blocks = append(blocks, block)
	}

	response := map[string]interface{}{
		"blocks": blocks,
		"page":   page,
		"limit":  limit,
		"total":  currentHeight,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GET /block/:height - 单个区块
func handleBlock(w http.ResponseWriter, r *http.Request) {
	height := strings.TrimPrefix(r.URL.Path, "/block/")

	data, err := proxyToNode("/block/" + height)
	if err != nil {
		http.Error(w, "Block not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// GET /transactions?page=1&limit=20 - 交易列表
func handleTransactions(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	// 获取当前高度
	statusData, err := proxyToNode("/status")
	if err != nil {
		http.Error(w, "Failed to get status", http.StatusInternalServerError)
		return
	}

	var status map[string]interface{}
	json.Unmarshal(statusData, &status)
	currentHeight := int(status["height"].(float64))

	// 收集交易
	transactions := []interface{}{}
	collected := 0
	skip := (page - 1) * limit
	skipped := 0

	for h := currentHeight; h >= 1 && collected < limit; h-- {
		blockData, err := proxyToNode(fmt.Sprintf("/block/%d", h))
		if err != nil {
			continue
		}

		var block map[string]interface{}
		json.Unmarshal(blockData, &block)

		if txs, ok := block["transactions"].([]interface{}); ok {
			for _, tx := range txs {
				if skipped < skip {
					skipped++
					continue
				}

				if collected >= limit {
					break
				}

				transactions = append(transactions, tx)
				collected++
			}
		}
	}

	response := map[string]interface{}{
		"transactions": transactions,
		"page":         page,
		"limit":        limit,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GET /transfers?page=1&limit=20&address=xxx - 转账列表（直接代理到fan-chain的/transfers API）
func handleTransfers(w http.ResponseWriter, r *http.Request) {
	// 构建查询参数
	queryParams := []string{}

	if page := r.URL.Query().Get("page"); page != "" {
		queryParams = append(queryParams, "page="+page)
	}
	if limit := r.URL.Query().Get("limit"); limit != "" {
		queryParams = append(queryParams, "limit="+limit)
	}
	if address := r.URL.Query().Get("address"); address != "" {
		queryParams = append(queryParams, "address="+address)
	}

	endpoint := "/transfers"
	if len(queryParams) > 0 {
		endpoint += "?" + strings.Join(queryParams, "&")
	}

	// 直接代理到fan-chain的/transfers API（使用数据库索引）
	data, err := proxyToNode(endpoint)
	if err != nil {
		log.Printf("Failed to get transfers from fan-chain: %v", err)
		http.Error(w, "Failed to get transfers", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// GET /balance/:address - 账户余额
func handleBalance(w http.ResponseWriter, r *http.Request) {
	address := strings.TrimPrefix(r.URL.Path, "/balance/")

	data, err := proxyToNode("/balance/" + address)
	if err != nil {
		http.Error(w, "Address not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// GET /accounts?page=1&limit=20 - 账户列表
func handleAccounts(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	// 从节点获取所有账户快照
	snapshotData, err := proxyToNode("/state/snapshot")
	if err != nil {
		http.Error(w, "Failed to get accounts", http.StatusInternalServerError)
		return
	}

	var snapshot map[string]interface{}
	if err := json.Unmarshal(snapshotData, &snapshot); err != nil {
		http.Error(w, "Failed to parse accounts", http.StatusInternalServerError)
		return
	}

	// 获取账户列表并为每个账户获取余额详情
	accounts := []interface{}{}
	if accountList, ok := snapshot["accounts"].([]interface{}); ok {
		for _, acc := range accountList {
			if accMap, ok := acc.(map[string]interface{}); ok {
				if addr, ok := accMap["address"].(string); ok {
					// 获取完整余额信息
					balanceData, err := proxyToNode("/balance/" + addr)
					if err == nil {
						var account map[string]interface{}
						json.Unmarshal(balanceData, &account)
						accounts = append(accounts, account)
					}
				}
			}
		}
	}

	// 按总余额（可用余额+质押余额）从大到小排序
	sort.Slice(accounts, func(i, j int) bool {
		accI := accounts[i].(map[string]interface{})
		accJ := accounts[j].(map[string]interface{})

		totalI := int64(0)
		totalJ := int64(0)

		if avail, ok := accI["available_balance"].(float64); ok {
			totalI += int64(avail)
		}
		if staked, ok := accI["staked_balance"].(float64); ok {
			totalI += int64(staked)
		}

		if avail, ok := accJ["available_balance"].(float64); ok {
			totalJ += int64(avail)
		}
		if staked, ok := accJ["staked_balance"].(float64); ok {
			totalJ += int64(staked)
		}

		return totalI > totalJ // 从大到小
	})

	// 分页处理
	start := (page - 1) * limit
	end := start + limit
	if start >= len(accounts) {
		accounts = []interface{}{}
	} else {
		if end > len(accounts) {
			end = len(accounts)
		}
		accounts = accounts[start:end]
	}

	response := map[string]interface{}{
		"accounts": accounts,
		"page":     page,
		"limit":    limit,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GET /transaction/:hash - 单个交易详情
func handleTransaction(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimPrefix(r.URL.Path, "/transaction/")

	// 获取当前高度
	statusData, err := proxyToNode("/status")
	if err != nil {
		http.Error(w, "Failed to get status", http.StatusInternalServerError)
		return
	}

	var status map[string]interface{}
	json.Unmarshal(statusData, &status)
	currentHeight := int(status["height"].(float64))

	// 遍历区块查找交易
	for h := currentHeight; h >= 1; h-- {
		blockData, err := proxyToNode(fmt.Sprintf("/block/%d", h))
		if err != nil {
			continue
		}

		var block map[string]interface{}
		json.Unmarshal(blockData, &block)

		if txs, ok := block["transactions"].([]interface{}); ok {
			for _, tx := range txs {
				txMap := tx.(map[string]interface{})
				if txHash, ok := txMap["hash"].(string); ok && txHash == hash {
					// 找到交易，添加区块信息
					txMap["block_height"] = block["height"]
					txMap["block_hash"] = block["hash"]

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(txMap)
					return
				}
			}
		}
	}

	http.Error(w, "Transaction not found", http.StatusNotFound)
}

// GET /account/:address/transactions?page=1&limit=20 - 账户交易历史
func handleAccountTransactions(w http.ResponseWriter, r *http.Request) {
	// 从路径中提取地址：/account/{address}/transactions
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/account/"), "/")
	if len(pathParts) < 2 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	address := pathParts[0]

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	// 获取当前高度
	statusData, err := proxyToNode("/status")
	if err != nil {
		http.Error(w, "Failed to get status", http.StatusInternalServerError)
		return
	}

	var status map[string]interface{}
	json.Unmarshal(statusData, &status)
	currentHeight := int(status["height"].(float64))

	// 收集该地址的交易
	transactions := []interface{}{}
	collected := 0
	skip := (page - 1) * limit
	skipped := 0

	for h := currentHeight; h >= 1 && collected < limit; h-- {
		blockData, err := proxyToNode(fmt.Sprintf("/block/%d", h))
		if err != nil {
			continue
		}

		var block map[string]interface{}
		json.Unmarshal(blockData, &block)

		if txs, ok := block["transactions"].([]interface{}); ok {
			for _, tx := range txs {
				txMap := tx.(map[string]interface{})

				// 检查是否与该地址相关
				from := ""
				to := ""
				if f, ok := txMap["from"].(string); ok {
					from = f
				}
				if t, ok := txMap["to"].(string); ok {
					to = t
				}

				if from == address || to == address {
					if skipped < skip {
						skipped++
						continue
					}

					if collected >= limit {
						break
					}

					// 添加区块信息
					txMap["block_height"] = block["height"]
					txMap["block_hash"] = block["hash"]

					transactions = append(transactions, txMap)
					collected++
				}
			}
		}
	}

	response := map[string]interface{}{
		"address":      address,
		"transactions": transactions,
		"page":         page,
		"limit":        limit,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GET /block/:height/data - 获取区块Data字段（base64编码）
func handleBlockData(w http.ResponseWriter, r *http.Request) {
	// 从路径中提取高度：/block/{height}/data
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/block/"), "/")
	if len(pathParts) < 2 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	height := pathParts[0]

	blockData, err := proxyToNode("/block/" + height)
	if err != nil {
		http.Error(w, "Block not found", http.StatusNotFound)
		return
	}

	var block map[string]interface{}
	json.Unmarshal(blockData, &block)

	// 提取Data字段
	dataField := ""
	dataSize := 0
	if data, ok := block["data"].(string); ok && data != "" {
		dataField = data
		dataSize = len(data)
	}

	response := map[string]interface{}{
		"height":    block["height"],
		"hash":      block["hash"],
		"data":      dataField,
		"data_size": dataSize,
		"has_data":  dataSize > 0,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GET /search?q=xxx - 搜索
func handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")

	if query == "" {
		http.Error(w, "Query required", http.StatusBadRequest)
		return
	}

	// 尝试作为区块高度
	if height, err := strconv.Atoi(query); err == nil {
		blockData, err := proxyToNode(fmt.Sprintf("/block/%d", height))
		if err == nil {
			var block map[string]interface{}
			json.Unmarshal(blockData, &block)

			response := map[string]interface{}{
				"type": "block",
				"data": block,
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
	}

	// 尝试作为地址
	if strings.HasPrefix(query, "F") && len(query) == 37 {
		balanceData, err := proxyToNode("/balance/" + query)
		if err == nil {
			var account map[string]interface{}
			json.Unmarshal(balanceData, &account)

			response := map[string]interface{}{
				"type": "account",
				"data": account,
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
	}

	// 作为交易哈希（暂不支持）
	http.Error(w, "Not found", http.StatusNotFound)
}

func main() {
	log.Println("FAN链API服务启动中...")
	log.Printf("监听端口: %s", SERVER_PORT)
	log.Printf("节点API: %s", NODE_API)

	// 启动WebSocket广播处理
	go handleBroadcast()

	// 初始化转账缓存（后台异步）
	go initTransfersCache()

	// 启动区块链监控
	go monitorBlockchain()

	// 路由
	http.HandleFunc("/ws", handleWebSocket)
	http.HandleFunc("/status", corsMiddleware(handleStatus))
	http.HandleFunc("/blocks", corsMiddleware(handleBlocks))
	http.HandleFunc("/block/", func(w http.ResponseWriter, r *http.Request) {
		// 检查是否请求Data字段：/block/{height}/data
		if strings.HasSuffix(r.URL.Path, "/data") {
			corsMiddleware(handleBlockData)(w, r)
		} else {
			corsMiddleware(handleBlock)(w, r)
		}
	})
	http.HandleFunc("/transactions", corsMiddleware(handleTransactions))
	http.HandleFunc("/transfers", corsMiddleware(handleTransfers))
	http.HandleFunc("/balance/", corsMiddleware(handleBalance))
	http.HandleFunc("/accounts", corsMiddleware(handleAccounts))
		http.HandleFunc("/transaction/", corsMiddleware(handleTransaction))
	http.HandleFunc("/account/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/transactions") {
			corsMiddleware(handleAccountTransactions)(w, r)
		} else {
			http.Error(w, "Not found", http.StatusNotFound)
		}
	})
	http.HandleFunc("/search", corsMiddleware(handleSearch))

	// 健康检查
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// 静态文件服务（浏览器前端）
	fs := http.FileServer(http.Dir("./web"))
	http.Handle("/", fs)

	// 启动服务器
	server := &http.Server{
		Addr:         ":" + SERVER_PORT,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Println("WebSocket服务已启动，监听 /ws")
	log.Println("区块链监控已启动，每2秒检查一次")
	log.Fatal(server.ListenAndServe())
}
