package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/syndtr/goleveldb/leveldb"
)

type Account struct {
	Address          string `json:"address"`
	AvailableBalance uint64 `json:"available_balance"`
	StakedBalance    uint64 `json:"staked_balance"`
	Nonce            uint64 `json:"nonce"`
	NodeType         int    `json:"node_type"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: fix_supply <db_path>")
		os.Exit(1)
	}

	db, err := leveldb.OpenFile(os.Args[1], nil)
	if err != nil {
		fmt.Printf("Failed to open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// 目标地址
	targetAddr := "F25gxrj3tppc07hunne7hztvde5gkaw78f3xa"
	key := append([]byte("a"), []byte(targetAddr)...)

	// 读取当前值
	data, err := db.Get(key, nil)
	if err != nil {
		fmt.Printf("Failed to get account: %v\n", err)
		os.Exit(1)
	}

	var acc Account
	if err := json.Unmarshal(data, &acc); err != nil {
		fmt.Printf("Failed to parse: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("修改前: %s\n", acc.Address)
	fmt.Printf("  可用: %d (%d FAN)\n", acc.AvailableBalance, acc.AvailableBalance/1000000)

	// 扣除 240 FAN = 240000000 最小单位
	deduct := uint64(240000000)
	acc.AvailableBalance -= deduct

	fmt.Printf("修改后:\n")
	fmt.Printf("  可用: %d (%d FAN)\n", acc.AvailableBalance, acc.AvailableBalance/1000000)

	// 写回
	newData, _ := json.Marshal(acc)
	if err := db.Put(key, newData, nil); err != nil {
		fmt.Printf("Failed to save: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n✅ 修改成功！总供应量现在应该是 1,400,000,000 FAN")
}
