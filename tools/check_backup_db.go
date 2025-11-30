package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// 前缀定义
var (
	blockPrefix        = []byte("b") // b{哈希} -> 区块数据
	heightToHashPrefix = []byte("h") // h{高度} -> 区块哈希
	transferPrefix     = []byte("x") // x{高度}{txHash} -> 转账记录
)

type TransferRecord struct {
	TxHash      string `json:"tx_hash"`
	From        string `json:"from"`
	To          string `json:"to"`
	Amount      uint64 `json:"amount"`
	GasFee      uint64 `json:"gas_fee"`
	BlockHeight uint64 `json:"block_height"`
	Timestamp   int64  `json:"timestamp"`
	Nonce       uint64 `json:"nonce"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法: check_backup_db <数据库路径>")
		fmt.Println("示例: check_backup_db ./backups/remote_data_20251126/node2_data/blockchain.db/blockchain.db")
		os.Exit(1)
	}

	dbPath := os.Args[1]

	// 打开数据库(只读)
	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		fmt.Printf("无法打开数据库: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Println("=== 备份数据库分析 ===")
	fmt.Printf("数据库路径: %s\n\n", dbPath)

	// 1. 检查区块高度范围
	fmt.Println("--- 区块高度分析 ---")
	minHeight := uint64(^uint64(0)) // 最大值
	maxHeight := uint64(0)
	blockCount := 0

	iter := db.NewIterator(util.BytesPrefix(heightToHashPrefix), nil)
	defer iter.Release()

	for iter.Next() {
		key := iter.Key()
		if len(key) != 9 { // h + 8字节高度
			continue
		}
		height := binary.BigEndian.Uint64(key[1:])
		if height < minHeight {
			minHeight = height
		}
		if height > maxHeight {
			maxHeight = height
		}
		blockCount++
	}

	if blockCount > 0 {
		fmt.Printf("最早区块: %d\n", minHeight)
		fmt.Printf("最新区块: %d\n", maxHeight)
		fmt.Printf("区块总数: %d\n", blockCount)
	} else {
		fmt.Println("未找到区块数据")
	}

	// 2. 检查转账索引
	fmt.Println("\n--- 转账索引分析 ---")
	transferIter := db.NewIterator(util.BytesPrefix(transferPrefix), nil)
	defer transferIter.Release()

	transfers := []TransferRecord{}
	for transferIter.Next() {
		var record TransferRecord
		if err := json.Unmarshal(transferIter.Value(), &record); err != nil {
			continue
		}
		transfers = append(transfers, record)
	}

	fmt.Printf("转账索引总数: %d\n", len(transfers))
	if len(transfers) > 0 {
		fmt.Println("\n转账详情:")
		for i, tx := range transfers {
			fmt.Printf("  %d. 高度 %d: %s -> %s, 金额 %.6f FAN\n",
				i+1, tx.BlockHeight, tx.From[:15]+"...", tx.To[:15]+"...", float64(tx.Amount)/1000000)
		}
	}

	// 3. 检查早期区块(1-100)是否存在
	fmt.Println("\n--- 早期区块检查 (1-100) ---")
	earlyBlocks := []uint64{}
	for h := uint64(1); h <= 100; h++ {
		key := make([]byte, 9)
		key[0] = heightToHashPrefix[0]
		binary.BigEndian.PutUint64(key[1:], h)
		if _, err := db.Get(key, nil); err == nil {
			earlyBlocks = append(earlyBlocks, h)
		}
	}

	if len(earlyBlocks) > 0 {
		fmt.Printf("找到早期区块: %v\n", earlyBlocks)
	} else {
		fmt.Println("区块1-100均不存在")
	}

	// 4. 扫描所有区块查找Type=0转账
	fmt.Println("\n--- 扫描区块内Type=0交易 ---")
	blockIter := db.NewIterator(util.BytesPrefix(blockPrefix), nil)
	defer blockIter.Release()

	type0Count := 0
	type0Txs := []string{}

	for blockIter.Next() {
		// 简单JSON解析查找Type=0的交易
		data := blockIter.Value()

		// 简单搜索 "type":0 或 "Type":0
		dataStr := string(data)
		if contains(dataStr, `"type":0`) || contains(dataStr, `"Type":0`) {
			// 找到可能包含Type=0交易的区块
			type0Count++
			// 提取一些信息
			if len(type0Txs) < 20 {
				// 提取区块高度
				heightStart := indexOf(dataStr, `"height":`)
				if heightStart >= 0 {
					heightEnd := indexOf(dataStr[heightStart:], ",")
					if heightEnd > 0 {
						heightStr := dataStr[heightStart : heightStart+heightEnd]
						type0Txs = append(type0Txs, heightStr)
					}
				}
			}
		}
	}

	fmt.Printf("包含Type=0交易的区块数: %d\n", type0Count)
	if len(type0Txs) > 0 {
		fmt.Println("示例区块高度:")
		for _, h := range type0Txs {
			fmt.Printf("  %s\n", h)
		}
	}
}

func contains(s, substr string) bool {
	return indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
