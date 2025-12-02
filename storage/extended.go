package storage

import (
	"encoding/binary"
	"fan-chain/core"
	"log"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// ========== 验证者存储 ==========

// 保存验证者信息
func (db *Database) SaveValidator(address string, publicKey []byte, stakedAmount uint64) error {
	data := make([]byte, len(publicKey)+8)
	copy(data, publicKey)
	binary.BigEndian.PutUint64(data[len(publicKey):], stakedAmount)

	key := append([]byte("v"), []byte(address)...)
	return db.db.Put(key, data, nil)
}

// 获取所有验证者
func (db *Database) GetAllValidators() (map[string][]byte, error) {
	validators := make(map[string][]byte)

	iter := db.db.NewIterator(util.BytesPrefix([]byte("v")), nil)
	defer iter.Release()

	for iter.Next() {
		address := string(iter.Key()[1:]) // 跳过前缀"v"
		publicKey := make([]byte, len(iter.Value())-8)
		copy(publicKey, iter.Value()[:len(iter.Value())-8])
		validators[address] = publicKey
	}

	return validators, iter.Error()
}

// ========== 对等节点存储 ==========

// 保存对等节点
func (db *Database) SavePeer(address string, lastSeen int64) error {
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, uint64(lastSeen))

	key := append([]byte("p"), []byte(address)...)
	return db.db.Put(key, data, nil)
}

// 获取所有对等节点
func (db *Database) GetAllPeers() (map[string]int64, error) {
	peers := make(map[string]int64)

	iter := db.db.NewIterator(util.BytesPrefix([]byte("p")), nil)
	defer iter.Release()

	for iter.Next() {
		address := string(iter.Key()[1:])
		lastSeen := int64(binary.BigEndian.Uint64(iter.Value()))
		peers[address] = lastSeen
	}

	return peers, iter.Error()
}

// ========== 区块时间戳索引 ==========

// 保存区块时间戳索引（在SaveBlock中调用）
func (db *Database) SaveBlockTimestamp(height uint64, timestamp int64) error {
	key := make([]byte, 9)
	key[0] = byte('s')
	binary.BigEndian.PutUint64(key[1:], uint64(timestamp))

	value := make([]byte, 8)
	binary.BigEndian.PutUint64(value, height)

	return db.db.Put(key, value, nil)
}

// ========== 区块剪枝（P4协议） ==========

// CleanupOldBlocks 清理旧区块（已废弃，改用PruneBlocks）
// 保留此函数签名以兼容旧代码调用，内部转发到PruneBlocks
func (d *Database) CleanupOldBlocks(currentTime int64) error {
	consensusConfig := core.GetConsensusConfig()
	retentionDays := consensusConfig.StorageParams.LedgerRetentionDays
	// 计算保留区块数：天数 × 每天区块数（86400秒/5秒 = 17280块/天）
	keepBlocks := uint64(retentionDays * 17280)

	count, err := d.PruneBlocks(keepBlocks)
	if err != nil {
		return err
	}

	if count > 0 {
		log.Printf("✓ PRUNE: Cleaned up %d chunk files (keeping %d days = %d blocks)", count, retentionDays, keepBlocks)
	}

	// 同时清理时间戳索引（s前缀）
	d.pruneTimestampIndex(currentTime, retentionDays)

	return nil
}

// pruneTimestampIndex 清理时间戳索引
func (d *Database) pruneTimestampIndex(currentTime int64, retentionDays int) {
	retentionTime := int64(retentionDays * 24 * 60 * 60)
	cutoffTime := currentTime - retentionTime

	iter := d.db.NewIterator(util.BytesPrefix([]byte("s")), nil)
	defer iter.Release()

	deletedCount := 0
	for iter.Next() {
		timestamp := int64(binary.BigEndian.Uint64(iter.Key()[1:]))
		if timestamp >= cutoffTime {
			break
		}
		d.db.Delete(iter.Key(), nil)
		deletedCount++
	}

	if deletedCount > 0 {
		log.Printf("✓ PRUNE: Cleaned up %d timestamp index entries", deletedCount)
	}
}

// 获取最旧区块的时间
func (db *Database) GetOldestBlockTime() (int64, error) {
	iter := db.db.NewIterator(util.BytesPrefix([]byte("s")), nil)
	defer iter.Release()

	if iter.First() {
		timestamp := int64(binary.BigEndian.Uint64(iter.Key()[1:]))
		return timestamp, nil
	}

	return 0, leveldb.ErrNotFound
}
