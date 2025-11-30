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

// ========== 100天自动清理 ==========

// 清理旧区块（根据配置的保留天数）
func (db *Database) CleanupOldBlocks(currentTime int64) error {
	// 从共识配置读取保留天数
	consensusConfig := core.GetConsensusConfig()
	retentionDays := consensusConfig.StorageParams.LedgerRetentionDays
	retentionTime := int64(retentionDays * 24 * 60 * 60) // 转换为秒
	cutoffTime := currentTime - retentionTime

	// 查找需要删除的区块
	iter := db.db.NewIterator(util.BytesPrefix([]byte("s")), nil)
	defer iter.Release()

	deletedCount := 0
	for iter.Next() {
		timestamp := int64(binary.BigEndian.Uint64(iter.Key()[1:]))
		if timestamp >= cutoffTime {
			break // 时间戳是递增的，后面的都不需要删除
		}

		height := binary.BigEndian.Uint64(iter.Value())

		// 删除区块
		block, err := db.GetBlockByHeight(height)
		if err == nil {
			// 删除区块数据
			blockKey := makeBlockKey(block.Hash())
			db.db.Delete(blockKey, nil)

			// 删除高度映射
			heightKey := makeHeightKey(height)
			db.db.Delete(heightKey, nil)

			// 删除时间戳索引
			db.db.Delete(iter.Key(), nil)

			deletedCount++
		}
	}

	if deletedCount > 0 {
		log.Printf("Cleaned up %d blocks older than %d days", deletedCount, consensusConfig.StorageParams.LedgerRetentionDays)
	}

	return iter.Error()
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
