package main

import (
	"fmt"
	"log"
	"os"

	"fan-chain/storage"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: count_rewards <data_dir>")
		os.Exit(1)
	}

	dataDir := os.Args[1]
	db, err := storage.OpenDatabase(dataDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	latestHeight, err := db.GetLatestHeight()
	if err != nil {
		log.Fatalf("Failed to get latest height: %v", err)
	}
	fmt.Printf("Latest height: %d\n", latestHeight)

	var totalRewards uint64
	rewardCount := 0

	for height := uint64(1); height <= latestHeight; height++ {
		block, err := db.GetBlockByHeight(height)
		if err != nil {
			continue
		}

		for _, tx := range block.Transactions {
			if tx.Type == 3 { // Reward
				totalRewards += tx.Amount
				rewardCount++
			}
		}
	}

	fmt.Printf("\nTotal Reward transactions: %d\n", rewardCount)
	fmt.Printf("Total Reward amount: %d\n", totalRewards)
	fmt.Printf("Average reward per block: %.2f\n", float64(totalRewards)/float64(rewardCount))
}
