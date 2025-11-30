package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// NonceManager 智能nonce管理器（类似MetaMask）
type NonceManager struct {
	nodeURL       string
	cacheFile     string
	pendingNonces map[string]uint64 // 地址 -> 已分配的最高nonce
	mu            sync.Mutex
}

// PendingNonceCache 本地nonce缓存
type PendingNonceCache struct {
	PendingNonces map[string]uint64 `json:"pending_nonces"`
}

// NewNonceManager 创建nonce管理器
func NewNonceManager(nodeURL string) *NonceManager {
	homeDir, _ := os.UserHomeDir()
	cacheFile := filepath.Join(homeDir, ".fan_nonce_cache.json")

	nm := &NonceManager{
		nodeURL:       nodeURL,
		cacheFile:     cacheFile,
		pendingNonces: make(map[string]uint64),
	}

	// 加载缓存
	nm.loadCache()

	return nm
}

// GetNextNonce 获取下一个可用nonce（智能管理）
func (nm *NonceManager) GetNextNonce(address string) (uint64, error) {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	// 1. 查询链上已确认的nonce
	confirmedNonce, err := nm.queryChainNonce(address)
	if err != nil {
		return 0, fmt.Errorf("查询链上nonce失败: %v", err)
	}

	// 2. 检查本地pending的最高nonce
	localPendingNonce, exists := nm.pendingNonces[address]

	// 3. 选择更大的nonce
	var nextNonce uint64
	if exists && localPendingNonce >= confirmedNonce {
		// 本地有未确认交易，使用本地nonce+1
		nextNonce = localPendingNonce + 1
	} else {
		// 使用链上nonce
		nextNonce = confirmedNonce
	}

	// 4. 记录新分配的nonce
	nm.pendingNonces[address] = nextNonce

	// 5. 保存缓存
	nm.saveCache()

	return nextNonce, nil
}

// ResetNonce 重置某地址的nonce缓存（交易确认后调用）
func (nm *NonceManager) ResetNonce(address string) {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	delete(nm.pendingNonces, address)
	nm.saveCache()
}

// ClearAllCache 清除所有nonce缓存
func (nm *NonceManager) ClearAllCache() {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	nm.pendingNonces = make(map[string]uint64)
	nm.saveCache()
}

// GetPendingCount 获取某地址的pending交易数
func (nm *NonceManager) GetPendingCount(address string) int {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	confirmedNonce, err := nm.queryChainNonce(address)
	if err != nil {
		return 0
	}

	localPendingNonce, exists := nm.pendingNonces[address]
	if !exists || localPendingNonce < confirmedNonce {
		return 0
	}

	return int(localPendingNonce - confirmedNonce + 1)
}

// queryChainNonce 查询链上确认的nonce
func (nm *NonceManager) queryChainNonce(address string) (uint64, error) {
	url := fmt.Sprintf("%s/balance/%s", nm.nodeURL, address)
	resp, err := http.Get(url)
	if err != nil {
		return 0, fmt.Errorf("HTTP请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("节点返回错误 (状态码=%d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("读取响应失败: %v", err)
	}

	var result struct {
		Nonce uint64 `json:"nonce"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("解析JSON失败: %v", err)
	}

	return result.Nonce, nil
}

// loadCache 加载本地缓存
func (nm *NonceManager) loadCache() {
	data, err := os.ReadFile(nm.cacheFile)
	if err != nil {
		// 缓存文件不存在或读取失败，使用空缓存
		return
	}

	var cache PendingNonceCache
	if err := json.Unmarshal(data, &cache); err != nil {
		// JSON解析失败，使用空缓存
		return
	}

	nm.pendingNonces = cache.PendingNonces
}

// saveCache 保存本地缓存
func (nm *NonceManager) saveCache() {
	cache := PendingNonceCache{
		PendingNonces: nm.pendingNonces,
	}

	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return
	}

	os.WriteFile(nm.cacheFile, data, 0644)
}

// ShowStatus 显示nonce状态
func (nm *NonceManager) ShowStatus(address string) {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	confirmedNonce, err := nm.queryChainNonce(address)
	if err != nil {
		fmt.Printf("查询链上nonce失败: %v\n", err)
		return
	}

	localPendingNonce, exists := nm.pendingNonces[address]

	fmt.Printf("\n=== Nonce状态 ===\n")
	fmt.Printf("地址: %s\n", address)
	fmt.Printf("链上已确认nonce: %d\n", confirmedNonce)
	if exists {
		fmt.Printf("本地pending最高nonce: %d\n", localPendingNonce)
		if localPendingNonce >= confirmedNonce {
			fmt.Printf("未确认交易数: %d\n", localPendingNonce-confirmedNonce+1)
		}
	} else {
		fmt.Printf("本地无pending交易\n")
	}
	fmt.Printf("下一个可用nonce: %d\n", max(confirmedNonce, localPendingNonce+1))
	fmt.Printf("===============\n\n")
}

func max(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
