package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// Account 账户结构（与 core.Account 一致）
type Account struct {
	Address          string `json:"address"`
	AvailableBalance uint64 `json:"available_balance"`
	StakedBalance    uint64 `json:"staked_balance"`
	Nonce            uint64 `json:"nonce"`
}

const (
	TotalSupply  = uint64(1400000000000000) // 1400万亿
	ShardCharset = "0123456789abcdefghijklmnopqrstuvwxyz"
)

func getShardKey(address string) string {
	if len(address) < 2 {
		return "0"
	}
	c := address[1]
	if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') {
		return string(c)
	}
	return "0"
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: migrate_to_sharded <data_dir>")
		fmt.Println("Example: migrate_to_sharded ./data")
		os.Exit(1)
	}

	dataDir := os.Args[1]
	legacyDBPath := filepath.Join(dataDir, "blockchain.db")
	stateDir := filepath.Join(dataDir, "state")

	// 检查旧数据库是否存在
	if _, err := os.Stat(legacyDBPath); os.IsNotExist(err) {
		fmt.Printf("错误: 旧数据库不存在: %s\n", legacyDBPath)
		os.Exit(1)
	}

	// 检查state目录是否已存在
	if _, err := os.Stat(stateDir); err == nil {
		fmt.Printf("警告: state目录已存在: %s\n", stateDir)
		fmt.Println("如果要重新迁移，请先删除该目录")
		os.Exit(1)
	}

	// 打开旧数据库
	legacyDB, err := leveldb.OpenFile(legacyDBPath, nil)
	if err != nil {
		fmt.Printf("错误: 无法打开旧数据库: %v\n", err)
		os.Exit(1)
	}
	defer legacyDB.Close()

	// 读取所有账户
	fmt.Println("=== 第1步: 读取旧数据库账户 ===")
	accounts := make(map[string]*Account)
	var totalBefore uint64

	iter := legacyDB.NewIterator(util.BytesPrefix([]byte("a")), nil)
	for iter.Next() {
		address := string(iter.Key()[1:]) // 跳过前缀"a"

		var account Account
		if err := json.Unmarshal(iter.Value(), &account); err != nil {
			fmt.Printf("警告: 无法解析账户 %s: %v\n", address, err)
			continue
		}

		accounts[address] = &account
		total := account.AvailableBalance + account.StakedBalance
		totalBefore += total

		fmt.Printf("  %s: 可用=%d 质押=%d 总计=%d\n",
			address, account.AvailableBalance/1000000, account.StakedBalance/1000000, total/1000000)
	}
	iter.Release()

	if err := iter.Error(); err != nil {
		fmt.Printf("错误: 迭代旧数据库失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n旧数据库账户数: %d\n", len(accounts))
	fmt.Printf("旧数据库总量: %d FAN (%d 基本单位)\n", totalBefore/1000000, totalBefore)

	// 验证总量
	if totalBefore != TotalSupply {
		fmt.Printf("\n!!! 错误: 总量不匹配 !!!\n")
		fmt.Printf("预期: %d\n", TotalSupply)
		fmt.Printf("实际: %d\n", totalBefore)
		fmt.Printf("差额: %d\n", int64(TotalSupply)-int64(totalBefore))
		os.Exit(1)
	}

	fmt.Println("\n✓ 旧数据库总量验证通过")

	// 创建36分片
	fmt.Println("\n=== 第2步: 创建36分片存储 ===")

	if err := os.MkdirAll(stateDir, 0755); err != nil {
		fmt.Printf("错误: 无法创建state目录: %v\n", err)
		os.Exit(1)
	}

	shards := make(map[string]*leveldb.DB)
	for _, c := range ShardCharset {
		shardKey := string(c)
		shardPath := filepath.Join(stateDir, fmt.Sprintf("shard_%s", shardKey))

		db, err := leveldb.OpenFile(shardPath, nil)
		if err != nil {
			fmt.Printf("错误: 无法创建分片 %s: %v\n", shardKey, err)
			os.Exit(1)
		}
		shards[shardKey] = db
	}
	defer func() {
		for _, db := range shards {
			db.Close()
		}
	}()

	fmt.Printf("创建了 %d 个分片\n", len(shards))

	// 迁移数据到分片
	fmt.Println("\n=== 第3步: 迁移账户到分片 ===")

	shardCounts := make(map[string]int)
	for address, account := range accounts {
		shardKey := getShardKey(address)
		db := shards[shardKey]

		data, err := json.Marshal(account)
		if err != nil {
			fmt.Printf("错误: 无法序列化账户 %s: %v\n", address, err)
			os.Exit(1)
		}

		if err := db.Put([]byte(address), data, nil); err != nil {
			fmt.Printf("错误: 无法写入账户 %s 到分片 %s: %v\n", address, shardKey, err)
			os.Exit(1)
		}

		shardCounts[shardKey]++
		fmt.Printf("  %s -> shard_%s\n", address, shardKey)
	}

	fmt.Println("\n分片分布:")
	for _, c := range ShardCharset {
		shardKey := string(c)
		if count, ok := shardCounts[shardKey]; ok && count > 0 {
			fmt.Printf("  shard_%s: %d 账户\n", shardKey, count)
		}
	}

	// 验证迁移后数据
	fmt.Println("\n=== 第4步: 验证迁移后数据 ===")

	var totalAfter uint64
	var countAfter int

	for _, c := range ShardCharset {
		shardKey := string(c)
		db := shards[shardKey]

		iter := db.NewIterator(nil, nil)
		for iter.Next() {
			var account Account
			if err := json.Unmarshal(iter.Value(), &account); err != nil {
				fmt.Printf("警告: 分片 %s 中无法解析账户: %v\n", shardKey, err)
				continue
			}

			total := account.AvailableBalance + account.StakedBalance
			totalAfter += total
			countAfter++
		}
		iter.Release()
	}

	fmt.Printf("\n迁移后账户数: %d\n", countAfter)
	fmt.Printf("迁移后总量: %d FAN (%d 基本单位)\n", totalAfter/1000000, totalAfter)

	// 最终验证
	if totalAfter != TotalSupply {
		fmt.Printf("\n!!! 错误: 迁移后总量不匹配 !!!\n")
		fmt.Printf("预期: %d\n", TotalSupply)
		fmt.Printf("实际: %d\n", totalAfter)
		os.Exit(1)
	}

	if countAfter != len(accounts) {
		fmt.Printf("\n!!! 错误: 迁移后账户数不匹配 !!!\n")
		fmt.Printf("预期: %d\n", len(accounts))
		fmt.Printf("实际: %d\n", countAfter)
		os.Exit(1)
	}

	fmt.Println("\n========================================")
	fmt.Println("✓ 迁移成功!")
	fmt.Printf("✓ 账户数: %d\n", countAfter)
	fmt.Printf("✓ 总量: %d FAN (恒定)\n", totalAfter/1000000)
	fmt.Println("========================================")
}
