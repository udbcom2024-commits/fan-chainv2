package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// 简化的区块结构
type Block struct {
	Header struct {
		Height    uint64 `json:"height"`
		Timestamp int64  `json:"timestamp"`
		Proposer  string `json:"proposer"`
	} `json:"header"`
	Transactions []Transaction `json:"transactions"`
}

type Transaction struct {
	Type      int    `json:"type"`
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    uint64 `json:"amount"`
	GasFee    uint64 `json:"gas_fee"`
	Nonce     uint64 `json:"nonce"`
}

const TOTAL_SUPPLY = uint64(1400000000000000)
const GENESIS_ADDRESS = "F25gxrj3tppc07hunne7hztvde5gkaw78f3xa"

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: analyze_supply <data_dir>")
		os.Exit(1)
	}

	dataDir := os.Args[1]
	blocksDir := filepath.Join(dataDir, "blocks")

	// 读取所有区块文件
	files, err := os.ReadDir(blocksDir)
	if err != nil {
		log.Fatalf("Failed to read blocks dir: %v", err)
	}

	// 按高度排序
	var heights []uint64
	blockFiles := make(map[uint64]string)

	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		// 解析高度 block_00000123.json
		name := strings.TrimPrefix(f.Name(), "block_")
		name = strings.TrimSuffix(name, ".json")
		h, err := strconv.ParseUint(name, 10, 64)
		if err != nil {
			continue
		}
		heights = append(heights, h)
		blockFiles[h] = filepath.Join(blocksDir, f.Name())
	}

	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })

	fmt.Printf("Found %d blocks\n", len(heights))

	// 初始化账户余额（创世状态）
	accounts := make(map[string]uint64) // 只追踪可用余额
	staked := make(map[string]uint64)   // 质押余额
	accounts[GENESIS_ADDRESS] = TOTAL_SUPPLY

	lastCorrectHeight := uint64(0)

	// 逐块分析
	for _, height := range heights {
		blockFile := blockFiles[height]
		data, err := os.ReadFile(blockFile)
		if err != nil {
			log.Printf("Failed to read block %d: %v", height, err)
			continue
		}

		var block Block
		if err := json.Unmarshal(data, &block); err != nil {
			log.Printf("Failed to parse block %d: %v", height, err)
			continue
		}

		// 处理每笔交易
		for _, tx := range block.Transactions {
			switch tx.Type {
			case 0: // Transfer
				if accounts[tx.From] < tx.Amount+tx.GasFee {
					fmt.Printf("Block %d: INSUFFICIENT BALANCE! %s has %d, needs %d\n",
						height, tx.From[:10], accounts[tx.From], tx.Amount+tx.GasFee)
				}
				accounts[tx.From] -= tx.Amount + tx.GasFee
				accounts[tx.To] += tx.Amount
				accounts[GENESIS_ADDRESS] += tx.GasFee // GAS费归创世

			case 1: // Stake
				if accounts[tx.From] < tx.Amount {
					fmt.Printf("Block %d: STAKE INSUFFICIENT! %s has %d, needs %d\n",
						height, tx.From[:10], accounts[tx.From], tx.Amount)
				}
				accounts[tx.From] -= tx.Amount
				staked[tx.From] += tx.Amount

			case 2: // Unstake
				if staked[tx.From] < tx.Amount {
					fmt.Printf("Block %d: UNSTAKE INSUFFICIENT! %s staked %d, needs %d\n",
						height, tx.From[:10], staked[tx.From], tx.Amount)
				}
				staked[tx.From] -= tx.Amount
				accounts[tx.From] += tx.Amount

			case 3: // Reward
				if tx.From == GENESIS_ADDRESS && tx.To == GENESIS_ADDRESS {
					// 自己给自己，不变
					continue
				}
				if tx.From == GENESIS_ADDRESS {
					accounts[GENESIS_ADDRESS] -= tx.Amount
					accounts[tx.To] += tx.Amount
				}

			case 4: // Slash
				accounts[tx.From] -= tx.Amount
				accounts[GENESIS_ADDRESS] += tx.Amount
			}
		}

		// 计算总量
		var total uint64
		for _, bal := range accounts {
			total += bal
		}
		for _, bal := range staked {
			total += bal
		}

		diff := int64(total) - int64(TOTAL_SUPPLY)

		if diff != 0 {
			fmt.Printf("🚨 Block %d: Total=%d, Diff=%d (proposer: %s)\n",
				height, total, diff, block.Header.Proposer[:10])

			// 打印该区块的所有交易
			fmt.Printf("   Transactions in this block:\n")
			for i, tx := range block.Transactions {
				fmt.Printf("   [%d] Type=%d, From=%s, To=%s, Amount=%d, GasFee=%d\n",
					i, tx.Type,
					truncate(tx.From, 10),
					truncate(tx.To, 10),
					tx.Amount, tx.GasFee)
			}
		} else {
			lastCorrectHeight = height
		}

		// 每1000块输出进度
		if height%1000 == 0 {
			fmt.Printf("Progress: Block %d, Total=%d, OK=%v\n", height, total, diff == 0)
		}
	}

	fmt.Printf("\n========================================\n")
	fmt.Printf("Last correct height: %d\n", lastCorrectHeight)
	fmt.Printf("========================================\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
