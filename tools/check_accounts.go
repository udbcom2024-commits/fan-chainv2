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
		fmt.Println("Usage: check_accounts <db_path>")
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
		total += acc.AvailableBalance + acc.StakedBalance
		count++
		if count <= 10 {
			fmt.Printf("%d. %s: avail=%d, staked=%d\n", count, acc.Address[:20], acc.AvailableBalance, acc.StakedBalance)
		}
	}

	fmt.Printf("\nTotal accounts: %d\n", count)
	fmt.Printf("Total supply: %d (expected: 1400000000000000)\n", total)
	fmt.Printf("Match: %v\n", total == 1400000000000000)
}
