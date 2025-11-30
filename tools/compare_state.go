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
		fmt.Println("Usage: compare_state <data_dir>")
		os.Exit(1)
	}

	dataDir := os.Args[1]

	// 打开数据库
	db, err := storage.OpenDatabase(dataDir)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	fmt.Println("========== 数据库中的账户状态 ==========")
	// 读取数据库中存储的账户
	dbAccounts, err := db.GetAllAccounts()
	if err != nil {
		log.Fatalf("Failed to get accounts: %v", err)
	}

	var dbTotal uint64
	type accInfo struct {
		addr    string
		balance uint64
		stake   uint64
	}
	var dbBalances []accInfo

	for _, acc := range dbAccounts {
		total := acc.AvailableBalance + acc.StakedBalance
		if total > 0 {
			dbBalances = append(dbBalances, accInfo{acc.Address, acc.AvailableBalance, acc.StakedBalance})
			dbTotal += total
		}
	}

	sort.Slice(dbBalances, func(i, j int) bool {
		return dbBalances[i].balance+dbBalances[i].stake > dbBalances[j].balance+dbBalances[j].stake
	})

	for _, b := range dbBalances {
		fmt.Printf("  %s: avail=%d, stake=%d, total=%d\n",
			truncate(b.addr, 20), b.balance, b.stake, b.balance+b.stake)
	}
	fmt.Printf("\n数据库总量: %d (预期: %d, 差值: %d)\n",
		dbTotal, TOTAL_SUPPLY, int64(dbTotal)-int64(TOTAL_SUPPLY))

	fmt.Println("\n========== 从区块推算的账户状态 ==========")

	// 获取最新高度
	latestHeight, err := db.GetLatestHeight()
	if err != nil {
		log.Fatalf("Failed to get latest height: %v", err)
	}
	fmt.Printf("Latest height: %d\n", latestHeight)

	// 找到第一个存在的区块
	var startHeight uint64
	for h := uint64(1); h <= latestHeight; h++ {
		_, err := db.GetBlockByHeight(h)
		if err == nil {
			startHeight = h
			break
		}
	}
	fmt.Printf("First available block: %d\n", startHeight)

	// 从checkpoint或快照开始（如果有的话）
	// 这里简化处理：从genesis开始计算
	accounts := make(map[string]uint64)
	staked := make(map[string]uint64)
	accounts[GENESIS_ADDRESS] = TOTAL_SUPPLY

	// 逐块分析
	for height := uint64(1); height <= latestHeight; height++ {
		block, err := db.GetBlockByHeight(height)
		if err != nil {
			continue // 跳过不存在的区块
		}

		// 处理每笔交易
		for _, tx := range block.Transactions {
			switch tx.Type {
			case 0: // Transfer
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
	}

	var calcTotal uint64
	var calcBalances []accInfo
	for addr := range accounts {
		total := accounts[addr] + staked[addr]
		if total > 0 {
			calcBalances = append(calcBalances, accInfo{addr, accounts[addr], staked[addr]})
			calcTotal += total
		}
	}

	sort.Slice(calcBalances, func(i, j int) bool {
		return calcBalances[i].balance+calcBalances[i].stake > calcBalances[j].balance+calcBalances[j].stake
	})

	for _, b := range calcBalances {
		fmt.Printf("  %s: avail=%d, stake=%d, total=%d\n",
			truncate(b.addr, 20), b.balance, b.stake, b.balance+b.stake)
	}
	fmt.Printf("\n推算总量: %d (预期: %d, 差值: %d)\n",
		calcTotal, TOTAL_SUPPLY, int64(calcTotal)-int64(TOTAL_SUPPLY))

	fmt.Println("\n========== 差异分析 ==========")
	// 对比差异
	for _, dbAcc := range dbBalances {
		calcBal := accounts[dbAcc.addr]
		calcStk := staked[dbAcc.addr]
		if dbAcc.balance != calcBal || dbAcc.stake != calcStk {
			fmt.Printf("差异! %s:\n", truncate(dbAcc.addr, 25))
			fmt.Printf("  数据库: avail=%d, stake=%d\n", dbAcc.balance, dbAcc.stake)
			fmt.Printf("  推算值: avail=%d, stake=%d\n", calcBal, calcStk)
			fmt.Printf("  差值:   avail=%d, stake=%d\n",
				int64(dbAcc.balance)-int64(calcBal), int64(dbAcc.stake)-int64(calcStk))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
