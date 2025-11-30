package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"fan-chain/storage"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: debug_block <data_dir> <height>")
		os.Exit(1)
	}

	dataDir := os.Args[1]
	var height uint64
	fmt.Sscanf(os.Args[2], "%d", &height)

	// 打开数据库
	db, err := storage.OpenDatabase(dataDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// 获取区块
	block, err := db.GetBlockByHeight(height)
	if err != nil {
		log.Fatalf("Failed to get block %d: %v", height, err)
	}

	fmt.Printf("Block #%d:\n", height)
	fmt.Printf("  Timestamp: %d\n", block.Header.Timestamp)
	fmt.Printf("  Proposer: %s\n", block.Header.Proposer)
	fmt.Printf("  Transactions: %d\n", len(block.Transactions))

	for i, tx := range block.Transactions {
		fmt.Printf("\n  [%d] Type=%d\n", i, tx.Type)
		fmt.Printf("      From: %s\n", tx.From)
		fmt.Printf("      To:   %s\n", tx.To)
		fmt.Printf("      Amount: %d\n", tx.Amount)
		fmt.Printf("      GasFee: %d\n", tx.GasFee)
	}

	// 打印完整JSON
	fmt.Println("\n\n=== Full Block JSON ===")
	data, _ := json.MarshalIndent(block, "", "  ")
	fmt.Println(string(data))
}
