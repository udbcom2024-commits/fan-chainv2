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

	// 直接扫描数据库获取所有存在的区块高度
	fmt.Println("Scanning database for blocks...")
	heights, err := db.GetAllBlockHeights()
	if err != nil {
		log.Fatalf("Failed to scan block heights: %v", err)
	}
	fmt.Printf("Found %d blocks in database\n", len(heights))

	if len(heights) == 0 {
		fmt.Println("No blocks found, nothing to rebuild")
		return
	}

	// 扫描所有区块并建立索引
	fmt.Println("Rebuilding transfer index...")
	count := 0
	processed := 0
	for _, height := range heights {
		block, err := db.GetBlockByHeight(height)
		if err != nil {
			continue // 跳过无法读取的区块
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

		processed++
		// 每1000块输出一次进度
		if processed%1000 == 0 {
			fmt.Printf("Progress: processed %d blocks, indexed %d transfers\n", processed, count)
		}
	}

	fmt.Printf("\nDone! Indexed %d transfers from %d blocks\n", count, len(heights))

	// 验证
	finalCount, _ := db.GetTransferCount()
	fmt.Printf("Final transfer index count: %d\n", finalCount)
}
