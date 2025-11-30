package state

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"fan-chain/core"
)

// CalculateStateRoot 计算状态树根哈希
// 通过对所有账户进行排序后构建Merkle树
func (sm *StateManager) CalculateStateRoot() (core.Hash, error) {
	// 获取所有账户
	accounts, err := sm.db.GetAllAccounts()
	if err != nil {
		return core.Hash{}, fmt.Errorf("failed to get accounts: %v", err)
	}

	// 创建账户映射，数据库账户为基础
	accountMap := make(map[string]*core.Account)
	for _, acc := range accounts {
		accountMap[acc.Address] = acc
	}

	// 用缓存中的账户覆盖数据库账户（缓存优先）
	for addr, cachedAcc := range sm.accountCache {
		accountMap[addr] = cachedAcc
	}

	// 将映射转换为切片
	mergedAccounts := make([]*core.Account, 0, len(accountMap))
	for _, acc := range accountMap {
		mergedAccounts = append(mergedAccounts, acc)
	}

	if len(mergedAccounts) == 0 {
		// 空状态返回零哈希
		return core.Hash{}, nil
	}

	// 按地址排序
	sort.Slice(mergedAccounts, func(i, j int) bool {
		return mergedAccounts[i].Address < mergedAccounts[j].Address
	})

	// 计算每个账户的哈希
	leaves := make([][]byte, len(mergedAccounts))
	for i, acc := range mergedAccounts {
		leaves[i] = hashAccount(acc)
	}

	// 构建Merkle树
	root := buildMerkleTree(leaves)

	var hash core.Hash
	copy(hash[:], root)
	return hash, nil
}

// hashAccount 计算单个账户的哈希
func hashAccount(acc *core.Account) []byte {
	data := fmt.Sprintf("%s:%d:%d:%d:%d",
		acc.Address,
		acc.AvailableBalance,
		acc.StakedBalance,
		acc.Nonce,
		acc.NodeType,
	)
	hash := sha256.Sum256([]byte(data))
	return hash[:]
}

// buildMerkleTree 构建Merkle树
func buildMerkleTree(leaves [][]byte) []byte {
	if len(leaves) == 0 {
		return make([]byte, 32)
	}

	if len(leaves) == 1 {
		return leaves[0]
	}

	// 如果是奇数个叶子，复制最后一个
	if len(leaves)%2 != 0 {
		leaves = append(leaves, leaves[len(leaves)-1])
	}

	// 计算父节点
	var parents [][]byte
	for i := 0; i < len(leaves); i += 2 {
		parent := hashPair(leaves[i], leaves[i+1])
		parents = append(parents, parent)
	}

	// 递归构建上层
	return buildMerkleTree(parents)
}

// hashPair 计算两个哈希的父哈希
func hashPair(left, right []byte) []byte {
	data := append(left, right...)
	hash := sha256.Sum256(data)
	return hash[:]
}

// VerifyStateRoot 验证状态根
func (sm *StateManager) VerifyStateRoot(expectedRoot core.Hash) error {
	actualRoot, err := sm.CalculateStateRoot()
	if err != nil {
		return err
	}

	if actualRoot != expectedRoot {
		return fmt.Errorf("state root mismatch: expected %s, got %s",
			hex.EncodeToString(expectedRoot[:]),
			hex.EncodeToString(actualRoot[:]))
	}

	return nil
}
