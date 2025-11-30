package main

import (
	"fmt"
	"log"
	"os"
	"sort"

	"fan-chain/storage"
)

const TOTAL_SUPPLY = uint64(1400000000000000)
const GENESIS_ADDRESS = "F25gxrj3tppc07hunne7hztvde5gkaw78f3xa"

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: find_leak <data_dir>")
		os.Exit(1)
	}

	dataDir := os.Args[1]

	// 打开数据库
	db, err := storage.OpenDatabase(dataDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// 获取最新高度
	latestHeight, err := db.GetLatestHeight()
	if err != nil {
		log.Fatalf("Failed to get latest height: %v", err)
	}
	fmt.Printf("Latest height: %d\n", latestHeight)

	// 初始化账户状态
	accounts := make(map[string]uint64) // 可用余额
	staked := make(map[string]uint64)   // 质押余额
	accounts[GENESIS_ADDRESS] = TOTAL_SUPPLY

	lastCorrectHeight := uint64(0)
	firstLeakHeight := uint64(0)
	leakTxDetail := ""

	// 逐块分析
	for height := uint64(1); height <= latestHeight; height++ {
		block, err := db.GetBlockByHeight(height)
		if err != nil {
			log.Printf("Failed to get block %d: %v", height, err)
			continue
		}

		prevTotal := calculateTotal(accounts, staked)

		// 处理每笔交易
		for txIdx, tx := range block.Transactions {
			switch tx.Type {
			case 0: // Transfer
				if accounts[tx.From] < tx.Amount+tx.GasFee {
					fmt.Printf("Block %d Tx %d: INSUFFICIENT! %s has %d, needs %d\n",
						height, txIdx, tx.From[:10], accounts[tx.From], tx.Amount+tx.GasFee)
				}
				accounts[tx.From] -= tx.Amount + tx.GasFee
				accounts[tx.To] += tx.Amount
				if tx.To != GENESIS_ADDRESS {
					accounts[GENESIS_ADDRESS] += tx.GasFee
				}

			case 1: // Stake
				accounts[tx.From] -= tx.Amount
				staked[tx.From] += tx.Amount

			case 2: // Unstake
				staked[tx.From] -= tx.Amount
				accounts[tx.From] += tx.Amount

			case 3: // Reward
				if tx.From == GENESIS_ADDRESS && tx.To == GENESIS_ADDRESS {
					continue // 自己给自己
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

		currentTotal := calculateTotal(accounts, staked)
		diff := int64(currentTotal) - int64(TOTAL_SUPPLY)

		if diff != 0 && firstLeakHeight == 0 {
			firstLeakHeight = height
			leakTxDetail = fmt.Sprintf("Block %d caused leak:\n", height)
			for i, tx := range block.Transactions {
				leakTxDetail += fmt.Sprintf("  [%d] Type=%d From=%s To=%s Amount=%d GasFee=%d\n",
					i, tx.Type, truncate(tx.From, 15), truncate(tx.To, 15), tx.Amount, tx.GasFee)
			}
			leakTxDetail += fmt.Sprintf("  Total before: %d, after: %d, diff: %d\n", prevTotal, currentTotal, diff)
		}

		if diff == 0 {
			lastCorrectHeight = height
		}

		// 每500块输出进度
		if height%500 == 0 {
			fmt.Printf("Block %d: Total=%d, Diff=%d\n", height, currentTotal, diff)
		}
	}

	fmt.Println("\n========== ANALYSIS RESULT ==========")
	fmt.Printf("Last correct height: %d\n", lastCorrectHeight)
	fmt.Printf("First leak height: %d\n", firstLeakHeight)
	if leakTxDetail != "" {
		fmt.Println(leakTxDetail)
	}

	// 打印最终账户状态
	fmt.Println("\nFinal account balances:")
	type accBal struct {
		addr    string
		balance uint64
		stake   uint64
	}
	var balances []accBal
	for addr := range accounts {
		if accounts[addr] > 0 || staked[addr] > 0 {
			balances = append(balances, accBal{addr, accounts[addr], staked[addr]})
		}
	}
	sort.Slice(balances, func(i, j int) bool {
		return balances[i].balance+balances[i].stake > balances[j].balance+balances[j].stake
	})
	for _, b := range balances {
		fmt.Printf("  %s: avail=%d, stake=%d\n", b.addr[:15], b.balance, b.stake)
	}
	fmt.Printf("\nTotal supply: %d (expected: %d, diff: %d)\n",
		calculateTotal(accounts, staked), TOTAL_SUPPLY,
		int64(calculateTotal(accounts, staked))-int64(TOTAL_SUPPLY))
}

func calculateTotal(accounts, staked map[string]uint64) uint64 {
	var total uint64
	for _, bal := range accounts {
		total += bal
	}
	for _, bal := range staked {
		total += bal
	}
	return total
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
