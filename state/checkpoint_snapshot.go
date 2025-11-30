package state

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"fan-chain/core"
)

// CheckpointSnapshot Checkpoint状态快照
type CheckpointSnapshot struct {
	Height   uint64          `json:"height"`
	Accounts []*core.Account `json:"accounts"`
}

// CreateCheckpointSnapshot 创建checkpoint状态快照
func (sm *StateManager) CreateCheckpointSnapshot(height uint64) (*CheckpointSnapshot, error) {
	accounts, err := sm.db.GetAllAccounts()
	if err != nil {
		return nil, fmt.Errorf("failed to get accounts: %v", err)
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

	return &CheckpointSnapshot{
		Height:   height,
		Accounts: mergedAccounts,
	}, nil
}

// Compress 压缩快照数据
func (snapshot *CheckpointSnapshot) Compress() ([]byte, error) {
	// 序列化为JSON
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal snapshot: %v", err)
	}

	// 压缩
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	if _, err := gzWriter.Write(data); err != nil {
		gzWriter.Close()
		return nil, fmt.Errorf("failed to compress: %v", err)
	}
	if err := gzWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %v", err)
	}

	return buf.Bytes(), nil
}

// SaveToFile 保存快照到文件（压缩）
func (snapshot *CheckpointSnapshot) SaveToFile(filepath string) error {
	// 序列化为JSON
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot: %v", err)
	}

	// 创建文件
	file, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}
	defer file.Close()

	// 压缩写入
	gzWriter := gzip.NewWriter(file)
	defer gzWriter.Close()

	if _, err := gzWriter.Write(data); err != nil {
		return fmt.Errorf("failed to write compressed data: %v", err)
	}

	return nil
}

// LoadCheckpointSnapshotFromFile 从文件加载快照
func LoadCheckpointSnapshotFromFile(filepath string) (*CheckpointSnapshot, error) {
	// 打开文件
	file, err := os.Open(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	// 解压读取
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()

	// 读取所有数据
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, gzReader); err != nil {
		return nil, fmt.Errorf("failed to read compressed data: %v", err)
	}

	// 反序列化
	var snapshot CheckpointSnapshot
	if err := json.Unmarshal(buf.Bytes(), &snapshot); err != nil {
		return nil, fmt.Errorf("failed to unmarshal snapshot: %v", err)
	}

	return &snapshot, nil
}

// ApplyCheckpointSnapshot 应用快照到状态管理器
func (sm *StateManager) ApplyCheckpointSnapshot(snapshot *CheckpointSnapshot) error {
	// P0: 应用快照前验证总量
	var totalSupply uint64
	for _, acc := range snapshot.Accounts {
		totalSupply += acc.AvailableBalance + acc.StakedBalance
	}

	expectedSupply := uint64(1400000000000000)
	if totalSupply != expectedSupply {
		log.Printf("🚨 P0违反：快照总量不正确！实际=%d，预期=%d", totalSupply, expectedSupply)
		log.Printf("⛔ 拒绝同步，快照总量必须正确！")
		return fmt.Errorf("snapshot total supply mismatch: got %d, expected %d", totalSupply, expectedSupply)
	}
	log.Printf("✅ P0验证通过：快照总量正确 = %d", totalSupply)

	// 清空现有状态
	if err := sm.db.ClearAllAccounts(); err != nil {
		return fmt.Errorf("failed to clear accounts: %v", err)
	}

	// 导入所有账户
	for _, acc := range snapshot.Accounts {
		if err := sm.db.SaveAccount(acc); err != nil {
			return fmt.Errorf("failed to save account %s: %v", acc.Address, err)
		}
	}

	// 清空缓存，确保数据一致性
	sm.accountCache = make(map[string]*core.Account)
	sm.dirtyAccounts = make(map[string]bool)

	return nil
}

// Serialize 序列化快照为字节（用于P2P传输）
func (snapshot *CheckpointSnapshot) Serialize() ([]byte, error) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}

	// 压缩
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	if _, err := gzWriter.Write(data); err != nil {
		return nil, err
	}
	if err := gzWriter.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// DeserializeCheckpointSnapshot 反序列化快照
func DeserializeCheckpointSnapshot(data []byte) (*CheckpointSnapshot, error) {
	// 解压
	gzReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gzReader.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, gzReader); err != nil {
		return nil, err
	}

	// 反序列化
	var snapshot CheckpointSnapshot
	if err := json.Unmarshal(buf.Bytes(), &snapshot); err != nil {
		return nil, err
	}

	return &snapshot, nil
}
