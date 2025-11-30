package main

import (
	"fmt"
	"log"
	"os"

	"fan-chain/storage"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: rebuild_transfer_index <data_dir>")
		fmt.Println("Example: rebuild_transfer_index ./data")
		os.Exit(1)
	}

	dataDir := os.Args[1]

	// 打开数据库
	db, err := storage.OpenDatabase(dataDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// 获取当前转账索引数量
	existingCount, _ := db.GetTransferCount()
	fmt.Printf("Existing transfer index count: %d\n", existingCount)

	// 获取最新高度
	latestHeight, err := db.GetLatestHeight()
	if err != nil {
		log.Fatalf("Failed to get latest height: %v", err)
	}
	fmt.Printf("Latest block height: %d\n", latestHeight)

	// 扫描所有区块并建立索引
	fmt.Println("Rebuilding transfer index...")
	count := 0
	for height := uint64(1); height <= latestHeight; height++ {
		block, err := db.GetBlockByHeight(height)
		if err != nil {
			continue // 跳过不存在的区块
		}

		for _, tx := range block.Transactions {
			if tx.Type == 0 { // 仅索引转账交易
				if err := db.SaveTransfer(tx, height); err != nil {
					log.Printf("Failed to save transfer at height %d: %v", height, err)
					continue
				}
				count++
			}
		}

		// 每1000块输出一次进度
		if height%1000 == 0 {
			fmt.Printf("Progress: height %d, indexed %d transfers\n", height, count)
		}
	}

	fmt.Printf("\nDone! Indexed %d transfers from %d blocks\n", count, latestHeight)

	// 验证
	finalCount, _ := db.GetTransferCount()
	fmt.Printf("Final transfer index count: %d\n", finalCount)
}
