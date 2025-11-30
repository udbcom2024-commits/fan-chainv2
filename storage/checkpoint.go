package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fan-chain/core"
)

// SaveCheckpoint 保存检查点到文件（实现T-D-C原子性协议）
// T-D-C协议：Temporary -> Delete -> Commit
// 确保任何时刻只有一个有效的checkpoint文件
func (db *Database) SaveCheckpoint(checkpoint *core.Checkpoint, dataDir string) error {
	checkpointDir := filepath.Join(dataDir, "checkpoints")

	// 创建目录
	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoint dir: %v", err)
	}

	// 序列化检查点
	data, err := checkpoint.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize checkpoint: %v", err)
	}

	// T步骤：写入临时文件
	tempFile := filepath.Join(checkpointDir, "checkpoint_new.tmp")
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp checkpoint: %v", err)
	}

	// 获取当前的checkpoint文件
	latestFile := filepath.Join(checkpointDir, "checkpoint_latest.dat")

	// D步骤：删除旧的checkpoint文件（如果存在）
	if _, err := os.Stat(latestFile); err == nil {
		// 旧文件存在，删除它
		if err := os.Remove(latestFile); err != nil {
			// 删除失败，清理临时文件
			os.Remove(tempFile)
			return fmt.Errorf("failed to remove old checkpoint: %v", err)
		}
	}

	// C步骤：原子性重命名（提交）
	if err := os.Rename(tempFile, latestFile); err != nil {
		// 重命名失败，系统处于不一致状态
		// 尝试恢复：检查临时文件是否还在
		return fmt.Errorf("failed to commit checkpoint: %v", err)
	}

	// 强制单点：删除所有历史checkpoint文件
	// 只保留checkpoint_latest.dat
	entries, _ := os.ReadDir(checkpointDir)
	for _, entry := range entries {
		if !entry.IsDir() {
			name := entry.Name()
			// 删除所有非latest的checkpoint和state文件
			if name != "checkpoint_latest.dat" && name != "state_latest.dat.gz" &&
			   (strings.HasPrefix(name, "checkpoint_") || strings.HasPrefix(name, "state_")) {
				os.Remove(filepath.Join(checkpointDir, name))
			}
		}
	}

	return nil
}

// LoadCheckpoint 从文件加载检查点
// 【强制单点Checkpoint】忽略height参数，总是加载最新的checkpoint
func (db *Database) LoadCheckpoint(height uint64, dataDir string) (*core.Checkpoint, error) {
	// 根据P2协议，只有一个checkpoint_latest.dat文件
	// 忽略height参数，总是返回最新的checkpoint
	return db.GetLatestCheckpoint(dataDir)
}

// GetLatestCheckpoint 获取最新的检查点（单点checkpoint设计）
func (db *Database) GetLatestCheckpoint(dataDir string) (*core.Checkpoint, error) {
	checkpointDir := filepath.Join(dataDir, "checkpoints")

	// 检查是否需要恢复未完成的事务
	if err := db.recoverCheckpointTransaction(checkpointDir); err != nil {
		return nil, fmt.Errorf("failed to recover checkpoint transaction: %v", err)
	}

	// 直接读取唯一的checkpoint文件
	latestFile := filepath.Join(checkpointDir, "checkpoint_latest.dat")

	data, err := os.ReadFile(latestFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 没有checkpoint
		}
		return nil, fmt.Errorf("failed to read checkpoint: %v", err)
	}

	checkpoint, err := core.DeserializeCheckpoint(data)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize checkpoint: %v", err)
	}

	return checkpoint, nil
}

// recoverCheckpointTransaction 恢复未完成的checkpoint事务
// 如果系统在T-D-C协议执行过程中崩溃，此函数会完成事务
func (db *Database) recoverCheckpointTransaction(checkpointDir string) error {
	tempFile := filepath.Join(checkpointDir, "checkpoint_new.tmp")
	latestFile := filepath.Join(checkpointDir, "checkpoint_latest.dat")

	// 检查是否有未完成的事务
	if _, err := os.Stat(tempFile); err == nil {
		// 临时文件存在，说明事务未完成

		// 如果latest文件不存在，说明在D步骤后崩溃
		if _, err := os.Stat(latestFile); os.IsNotExist(err) {
			// 完成C步骤：将临时文件重命名为latest
			if err := os.Rename(tempFile, latestFile); err != nil {
				return fmt.Errorf("failed to recover checkpoint: %v", err)
			}
		} else {
			// latest文件存在，说明在T步骤后崩溃，删除临时文件
			os.Remove(tempFile)
		}
	}

	return nil
}

// ListCheckpoints 列出所有检查点（按高度降序）
// 【强制单点Checkpoint】只返回最新的checkpoint
func (db *Database) ListCheckpoints(dataDir string) ([]uint64, error) {
	// 根据P2协议，只有一个checkpoint_latest.dat文件
	checkpoint, err := db.GetLatestCheckpoint(dataDir)
	if err != nil {
		return nil, err
	}

	if checkpoint == nil {
		return nil, nil
	}

	// 返回最新checkpoint的高度
	return []uint64{checkpoint.Height}, nil
}

// CleanOldCheckpoints 清理旧检查点，只保留最新的N个
func (db *Database) CleanOldCheckpoints(dataDir string, keepCount int) error {
	heights, err := db.ListCheckpoints(dataDir)
	if err != nil {
		return err
	}

	if len(heights) <= keepCount {
		return nil // 无需清理
	}

	checkpointDir := filepath.Join(dataDir, "checkpoints")

	// 删除多余的检查点
	for i := keepCount; i < len(heights); i++ {
		height := heights[i]

		// 删除checkpoint文件
		checkpointFile := filepath.Join(checkpointDir, fmt.Sprintf("checkpoint_%d.dat", height))
		os.Remove(checkpointFile)

		// 删除对应的state文件
		stateFile := filepath.Join(checkpointDir, fmt.Sprintf("state_%d.dat.gz", height))
		os.Remove(stateFile)
	}

	return nil
}

// SaveStateSnapshot 保存状态快照到文件（强制单点）
func (db *Database) SaveStateSnapshot(height uint64, snapshotData []byte, dataDir string) error {
	checkpointDir := filepath.Join(dataDir, "checkpoints")

	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoint dir: %v", err)
	}

	// T步骤：写入临时文件
	tempFile := filepath.Join(checkpointDir, "state_new.tmp.gz")
	if err := os.WriteFile(tempFile, snapshotData, 0644); err != nil {
		return fmt.Errorf("failed to write temp state snapshot: %v", err)
	}

	// 获取当前的state文件
	latestFile := filepath.Join(checkpointDir, "state_latest.dat.gz")

	// D步骤：删除旧的state文件（如果存在）
	if _, err := os.Stat(latestFile); err == nil {
		if err := os.Remove(latestFile); err != nil {
			os.Remove(tempFile)
			return fmt.Errorf("failed to remove old state snapshot: %v", err)
		}
	}

	// C步骤：原子性重命名
	if err := os.Rename(tempFile, latestFile); err != nil {
		return fmt.Errorf("failed to commit state snapshot: %v", err)
	}

	return nil
}

// LoadStateSnapshot 加载状态快照（单点设计，忽略height参数）
func (db *Database) LoadStateSnapshot(height uint64, dataDir string) ([]byte, error) {
	checkpointDir := filepath.Join(dataDir, "checkpoints")

	// 总是加载唯一的latest状态快照
	latestFile := filepath.Join(checkpointDir, "state_latest.dat.gz")

	data, err := os.ReadFile(latestFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read state snapshot: %v", err)
	}

	return data, nil
}
