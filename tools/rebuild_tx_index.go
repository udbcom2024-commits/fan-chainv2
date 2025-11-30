package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"fan-chain/storage"
)

func main() {
	dataDir := flag.String("data", "./data", "数据目录路径")
	startHeight := flag.Uint64("start", 1, "起始区块高度")
	endHeight := flag.Uint64("end", 0, "结束区块高度（0表示最新）")
	batchSize := flag.Int("batch", 100, "每批处理的区块数")

	flag.Parse()

	fmt.Println("FAN链交易索引重建工具")
	fmt.Println("========================")
	fmt.Println()

	// 打开数据库
	dbPath := filepath.Join(*dataDir, "blockchain.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Fatalf("数据库不存在: %s", dbPath)
	}

	db, err := storage.OpenDatabase(*dataDir)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	// 获取最新高度
	latestHeight, err := db.GetLatestHeight()
	if err != nil {
		log.Fatalf("获取最新高度失败: %v", err)
	}

	if *endHeight == 0 || *endHeight > latestHeight {
		*endHeight = latestHeight
	}

	fmt.Printf("数据库路径: %s\n", dbPath)
	fmt.Printf("当前区块高度: %d\n", latestHeight)
	fmt.Printf("重建范围: %d - %d\n", *startHeight, *endHeight)
	fmt.Printf("批次大小: %d\n", *batchSize)
	fmt.Println()

	totalBlocks := *endHeight - *startHeight + 1
	totalTxs := uint64(0)
	processedBlocks := uint64(0)
	startTime := time.Now()

	fmt.Println("开始重建交易索引...")
	fmt.Println()

	// 分批处理
	for height := *startHeight; height <= *endHeight; height++ {
		// 获取区块
		block, err := db.GetBlockByHeight(height)
		if err != nil {
			log.Printf("警告：获取区块 %d 失败: %v", height, err)
			continue
		}

		// 为区块内的每笔交易创建索引
		for _, tx := range block.Transactions {
			if err := db.SaveTransaction(tx); err != nil {
				log.Printf("警告：保存交易索引失败 (区块=%d, 交易=%x): %v",
					height, tx.Hash().Bytes(), err)
			} else {
				totalTxs++
			}
		}

		processedBlocks++

		// 每处理一批显示进度
		if processedBlocks%uint64(*batchSize) == 0 {
			elapsed := time.Since(startTime)
			progress := float64(processedBlocks) / float64(totalBlocks) * 100
			speed := float64(processedBlocks) / elapsed.Seconds()
			remaining := time.Duration(float64(totalBlocks-processedBlocks)/speed) * time.Second

			fmt.Printf("进度: %d/%d (%.1f%%) | 交易数: %d | 速度: %.0f块/秒 | 预计剩余: %s\n",
				processedBlocks, totalBlocks, progress, totalTxs, speed, remaining.Round(time.Second))
		}
	}

	elapsed := time.Since(startTime)
	fmt.Println()
	fmt.Println("重建完成！")
	fmt.Printf("处理区块数: %d\n", processedBlocks)
	fmt.Printf("索引交易数: %d\n", totalTxs)
	fmt.Printf("总耗时: %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("平均速度: %.0f 块/秒\n", float64(processedBlocks)/elapsed.Seconds())
}
