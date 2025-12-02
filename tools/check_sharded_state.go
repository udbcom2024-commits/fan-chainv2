package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/syndtr/goleveldb/leveldb"
)

type Account struct {
	Address          string `json:"address"`
	AvailableBalance uint64 `json:"available_balance"`
	StakedBalance    uint64 `json:"staked_balance"`
	Nonce            uint64 `json:"nonce"`
}

const (
	TotalSupply  = uint64(1400000000000000)
	ShardCharset = "0123456789abcdefghijklmnopqrstuvwxyz"
	FANUnit      = uint64(1000000)
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: check_sharded_state <state_dir>")
		fmt.Println("Example: check_sharded_state ./data/state")
		os.Exit(1)
	}

	stateDir := os.Args[1]

	var totalSupply uint64
	var totalAccounts int
	accounts := make([]*Account, 0)

	for _, c := range ShardCharset {
		shardKey := string(c)
		shardPath := filepath.Join(stateDir, fmt.Sprintf("shard_%s", shardKey))

		db, err := leveldb.OpenFile(shardPath, nil)
		if err != nil {
			fmt.Printf("警告: 无法打开分片 %s: %v\n", shardKey, err)
			continue
		}

		iter := db.NewIterator(nil, nil)
		for iter.Next() {
			var acc Account
			if err := json.Unmarshal(iter.Value(), &acc); err != nil {
				fmt.Printf("警告: 分片 %s 解析失败: %v\n", shardKey, err)
				continue
			}
			accounts = append(accounts, &acc)
			totalSupply += acc.AvailableBalance + acc.StakedBalance
			totalAccounts++
		}
		iter.Release()
		db.Close()
	}

	// 按地址排序
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].Address < accounts[j].Address
	})

	// 打印账户
	for i, acc := range accounts {
		total := acc.AvailableBalance + acc.StakedBalance
		fmt.Printf("%d. %s\n", i+1, acc.Address)
		fmt.Printf("   可用: %d FAN  质押: %d FAN  总计: %d FAN\n",
			acc.AvailableBalance/FANUnit,
			acc.StakedBalance/FANUnit,
			total/FANUnit)
	}

	fmt.Println("\n========================================")
	fmt.Printf("账户总数: %d\n", totalAccounts)
	fmt.Printf("实际总量: %d FAN\n", totalSupply/FANUnit)
	fmt.Printf("预期总量: %d FAN\n", TotalSupply/FANUnit)
	fmt.Printf("差额: %d FAN\n", int64(totalSupply/FANUnit)-int64(TotalSupply/FANUnit))
}
