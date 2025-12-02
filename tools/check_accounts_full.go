package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

var accountPrefix = []byte("a")

type Account struct {
	Address          string `json:"address"`
	AvailableBalance uint64 `json:"available_balance"`
	StakedBalance    uint64 `json:"staked_balance"`
	Nonce            uint64 `json:"nonce"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: check_accounts_full <db_path>")
		os.Exit(1)
	}

	db, err := leveldb.OpenFile(os.Args[1], nil)
	if err != nil {
		fmt.Printf("Failed to open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	iter := db.NewIterator(util.BytesPrefix(accountPrefix), nil)
	defer iter.Release()

	total := uint64(0)
	count := 0
	for iter.Next() {
		var acc Account
		if err := json.Unmarshal(iter.Value(), &acc); err != nil {
			fmt.Printf("Failed to parse: %v\n", err)
			continue
		}
		subtotal := acc.AvailableBalance + acc.StakedBalance
		total += subtotal
		count++
		// 转换为FAN单位（1 FAN = 1000000）
		availFAN := acc.AvailableBalance / 1000000
		stakedFAN := acc.StakedBalance / 1000000
		totalFAN := subtotal / 1000000
		fmt.Printf("%d. %s\n", count, acc.Address)
		fmt.Printf("   可用: %d FAN  质押: %d FAN  总计: %d FAN\n", availFAN, stakedFAN, totalFAN)
	}

	totalFAN := total / 1000000
	fmt.Printf("\n========================================\n")
	fmt.Printf("账户总数: %d\n", count)
	fmt.Printf("实际总量: %d FAN\n", totalFAN)
	fmt.Printf("预期总量: 1400000000 FAN\n")
	fmt.Printf("差额: %d FAN\n", int64(totalFAN)-1400000000)
}
