package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"fan-chain/core"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// ShardedStateStore 36分片账户状态存储
// 按fan.md规范：按地址第二个字符（0-9a-z）分片
// 分片定位 O(1)：shard := address[1]
//
// 目录结构：
// data/state/
// ├── shard_0/   # F0xxxxxx... 的账户
// ├── shard_1/   # F1xxxxxx... 的账户
// ├── ...
// ├── shard_9/   # F9xxxxxx... 的账户
// ├── shard_a/   # Faxxxxxx... 的账户
// ├── ...
// └── shard_z/   # Fzxxxxxx... 的账户
//
// 容量规划：
// - LevelDB单实例：千万级key无压力
// - 36分片 × 千万级/分片 = 3.6亿账户（单机）
// - 分布式部署后，理论无上限

const (
	StateSubdir     = "state"
	ShardCount      = 36 // 0-9 (10个) + a-z (26个) = 36个分片
	ShardCharset    = "0123456789abcdefghijklmnopqrstuvwxyz"
	StateHeightFile = "state_height" // state高度文件，用于原子性恢复
)

// ShardedStateStore 分片状态存储
type ShardedStateStore struct {
	dataDir   string                 // 数据根目录
	stateDir  string                 // state子目录
	shards    map[string]*leveldb.DB // 36个分片的LevelDB实例
	mu        sync.RWMutex           // 读写锁
}

// NewShardedStateStore 创建分片状态存储
func NewShardedStateStore(dataDir string) (*ShardedStateStore, error) {
	stateDir := filepath.Join(dataDir, StateSubdir)

	// 创建state目录
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %v", err)
	}

	store := &ShardedStateStore{
		dataDir:  dataDir,
		stateDir: stateDir,
		shards:   make(map[string]*leveldb.DB),
	}

	// 打开所有36个分片
	for _, c := range ShardCharset {
		shardKey := string(c)
		shardPath := filepath.Join(stateDir, fmt.Sprintf("shard_%s", shardKey))

		db, err := leveldb.OpenFile(shardPath, nil)
		if err != nil {
			// 关闭已打开的分片
			store.Close()
			return nil, fmt.Errorf("failed to open shard %s: %v", shardKey, err)
		}
		store.shards[shardKey] = db
	}

	return store, nil
}

// Close 关闭所有分片
func (s *ShardedStateStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var lastErr error
	for key, db := range s.shards {
		if err := db.Close(); err != nil {
			lastErr = fmt.Errorf("failed to close shard %s: %v", key, err)
		}
	}
	s.shards = make(map[string]*leveldb.DB)
	return lastErr
}

// getShardKey 获取地址对应的分片键
// O(1)定位：直接取地址第二个字符
func getShardKey(address string) string {
	if len(address) < 2 {
		return "0" // 默认分片
	}
	c := address[1]
	// 验证是否为有效的分片字符（0-9, a-z）
	if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') {
		return string(c)
	}
	return "0" // 无效字符默认到分片0
}

// getShard 获取地址对应的分片数据库
func (s *ShardedStateStore) getShard(address string) (*leveldb.DB, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	shardKey := getShardKey(address)
	db, ok := s.shards[shardKey]
	if !ok {
		return nil, fmt.Errorf("shard %s not found", shardKey)
	}
	return db, nil
}

// SaveAccount 保存账户到对应分片
func (s *ShardedStateStore) SaveAccount(account *core.Account) error {
	db, err := s.getShard(account.Address)
	if err != nil {
		return err
	}

	data, err := account.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize account: %v", err)
	}

	return db.Put([]byte(account.Address), data, nil)
}

// SaveAccountsBatch 批量原子写入账户（跨分片）
// 按分片分组后，每个分片使用WriteBatch原子写入
func (s *ShardedStateStore) SaveAccountsBatch(accounts []*core.Account) error {
	if len(accounts) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 按分片分组
	shardBatches := make(map[string]*leveldb.Batch)
	for _, account := range accounts {
		shardKey := getShardKey(account.Address)

		if _, ok := shardBatches[shardKey]; !ok {
			shardBatches[shardKey] = new(leveldb.Batch)
		}

		data, err := account.ToJSON()
		if err != nil {
			return fmt.Errorf("failed to serialize account %s: %v", account.Address, err)
		}
		shardBatches[shardKey].Put([]byte(account.Address), data)
	}

	// 顺序提交所有batch
	for shardKey, batch := range shardBatches {
		db := s.shards[shardKey]
		if err := db.Write(batch, nil); err != nil {
			return fmt.Errorf("failed to write batch to shard %s: %v", shardKey, err)
		}
	}

	return nil
}

// GetAccount 从对应分片获取账户
func (s *ShardedStateStore) GetAccount(address string) (*core.Account, error) {
	db, err := s.getShard(address)
	if err != nil {
		return nil, err
	}

	data, err := db.Get([]byte(address), nil)
	if err != nil {
		if err == leveldb.ErrNotFound {
			// 账户不存在，返回新账户
			return core.NewAccount(address), nil
		}
		return nil, err
	}

	var account core.Account
	if err := json.Unmarshal(data, &account); err != nil {
		return nil, fmt.Errorf("failed to deserialize account: %v", err)
	}

	return &account, nil
}

// GetAllAccounts 获取所有分片中的所有账户
func (s *ShardedStateStore) GetAllAccounts() ([]*core.Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	accounts := []*core.Account{}

	// 遍历所有分片
	for _, db := range s.shards {
		iter := db.NewIterator(nil, nil)
		for iter.Next() {
			var account core.Account
			if err := json.Unmarshal(iter.Value(), &account); err != nil {
				continue
			}
			accounts = append(accounts, &account)
		}
		iter.Release()
		if err := iter.Error(); err != nil {
			return nil, err
		}
	}

	return accounts, nil
}

// ClearAllAccounts 清空所有分片中的账户
func (s *ShardedStateStore) ClearAllAccounts() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for shardKey, db := range s.shards {
		iter := db.NewIterator(nil, nil)
		batch := new(leveldb.Batch)
		for iter.Next() {
			batch.Delete(iter.Key())
		}
		iter.Release()

		if err := iter.Error(); err != nil {
			return fmt.Errorf("failed to iterate shard %s: %v", shardKey, err)
		}

		if err := db.Write(batch, nil); err != nil {
			return fmt.Errorf("failed to clear shard %s: %v", shardKey, err)
		}
	}

	return nil
}

// GetShardStats 获取分片统计信息
func (s *ShardedStateStore) GetShardStats() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[string]int)

	for shardKey, db := range s.shards {
		count := 0
		iter := db.NewIterator(nil, nil)
		for iter.Next() {
			count++
		}
		iter.Release()
		stats[shardKey] = count
	}

	return stats
}

// GetAccountsByPrefix 获取指定前缀的账户（用于分片内查询）
func (s *ShardedStateStore) GetAccountsByPrefix(prefix string) ([]*core.Account, error) {
	if len(prefix) < 2 {
		return nil, fmt.Errorf("prefix too short")
	}

	db, err := s.getShard(prefix)
	if err != nil {
		return nil, err
	}

	accounts := []*core.Account{}
	iter := db.NewIterator(util.BytesPrefix([]byte(prefix)), nil)
	defer iter.Release()

	for iter.Next() {
		var account core.Account
		if err := json.Unmarshal(iter.Value(), &account); err != nil {
			continue
		}
		accounts = append(accounts, &account)
	}

	return accounts, iter.Error()
}

// MigrateFromLegacy 从旧版单LevelDB迁移到36分片
// 用于升级现有数据
func (s *ShardedStateStore) MigrateFromLegacy(legacyDB *leveldb.DB) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 按分片分组的batch
	shardBatches := make(map[string]*leveldb.Batch)
	for _, c := range ShardCharset {
		shardBatches[string(c)] = new(leveldb.Batch)
	}

	// 遍历旧数据库中的账户
	accountPrefix := []byte("a")
	iter := legacyDB.NewIterator(util.BytesPrefix(accountPrefix), nil)
	defer iter.Release()

	count := 0
	for iter.Next() {
		// 解析地址（去掉前缀）
		address := string(iter.Key()[1:])
		data := iter.Value()

		// 确定目标分片
		shardKey := getShardKey(address)
		shardBatches[shardKey].Put([]byte(address), data)
		count++
	}

	if err := iter.Error(); err != nil {
		return 0, fmt.Errorf("failed to iterate legacy db: %v", err)
	}

	// 写入所有分片
	for shardKey, batch := range shardBatches {
		if batch.Len() > 0 {
			db := s.shards[shardKey]
			if err := db.Write(batch, nil); err != nil {
				return count, fmt.Errorf("failed to write to shard %s: %v", shardKey, err)
			}
		}
	}

	return count, nil
}

// ========== State Height 管理（原子性恢复） ==========

// GetStateHeight 获取state对应的区块高度
func (s *ShardedStateStore) GetStateHeight() (uint64, error) {
	heightFile := filepath.Join(s.stateDir, StateHeightFile)
	data, err := os.ReadFile(heightFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // 文件不存在，返回0
		}
		return 0, err
	}
	if len(data) < 8 {
		return 0, nil
	}
	return binary.BigEndian.Uint64(data), nil
}

// SaveStateHeight 保存state对应的区块高度
func (s *ShardedStateStore) SaveStateHeight(height uint64) error {
	heightFile := filepath.Join(s.stateDir, StateHeightFile)
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, height)
	return os.WriteFile(heightFile, data, 0644)
}

// SaveAccountsBatchWithHeight 批量保存账户并更新高度（原子性操作）
// 先写入所有分片，最后更新高度文件
func (s *ShardedStateStore) SaveAccountsBatchWithHeight(accounts []*core.Account, height uint64) error {
	if len(accounts) == 0 {
		// 即使没有账户变更，也要更新高度
		return s.SaveStateHeight(height)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 按分片分组
	shardBatches := make(map[string]*leveldb.Batch)
	for _, account := range accounts {
		shardKey := getShardKey(account.Address)

		if _, ok := shardBatches[shardKey]; !ok {
			shardBatches[shardKey] = new(leveldb.Batch)
		}

		data, err := account.ToJSON()
		if err != nil {
			return fmt.Errorf("failed to serialize account %s: %v", account.Address, err)
		}
		shardBatches[shardKey].Put([]byte(account.Address), data)
	}

	// 顺序提交所有batch
	for shardKey, batch := range shardBatches {
		db := s.shards[shardKey]
		if err := db.Write(batch, nil); err != nil {
			return fmt.Errorf("failed to write batch to shard %s: %v", shardKey, err)
		}
	}

	// 最后更新高度文件（作为提交完成的标记）
	return s.SaveStateHeight(height)
}
