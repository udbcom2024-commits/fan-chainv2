package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"path/filepath"

	"fan-chain/core"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// 数据库键前缀
var (
	blockPrefix    = []byte("b") // 区块
	heightPrefix   = []byte("h") // 高度->哈希
	accountPrefix  = []byte("a") // 账户
	txPrefix       = []byte("t") // 交易
	metaPrefix     = []byte("m") // 元数据
	transferPrefix = []byte("x") // 转账索引 (Type=0)
)

// 数据库
type Database struct {
	db *leveldb.DB
}

// 打开数据库
func OpenDatabase(dataDir string) (*Database, error) {
	dbPath := filepath.Join(dataDir, "blockchain.db")
	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %v", err)
	}

	return &Database{db: db}, nil
}

// 关闭数据库
func (db *Database) Close() error {
	return db.db.Close()
}

// ========== 区块存储 ==========

// 保存区块
func (db *Database) SaveBlock(block *core.Block) error {
	// 1. 序列化区块
	data, err := block.ToJSON()
	if err != nil {
		return err
	}

	// 2. 保存区块数据（key: b{hash} -> block）
	blockKey := makeBlockKey(block.Hash())
	if err := db.db.Put(blockKey, data, nil); err != nil {
		return err
	}

	// 3. 保存高度映射（key: h{height} -> hash）
	heightKey := makeHeightKey(block.Header.Height)
	hashBytes := block.Hash().Bytes()
	if err := db.db.Put(heightKey, hashBytes, nil); err != nil {
		return err
	}

	// 4. 更新最新高度
	if err := db.SaveLatestHeight(block.Header.Height); err != nil {
		return err
	}
	// 5. 保存时间戳索引
	db.SaveBlockTimestamp(block.Header.Height, block.Header.Timestamp)

	// 6. 保存区块内的所有交易到交易索引
	for _, tx := range block.Transactions {
		if err := db.SaveTransaction(tx); err != nil {
			return fmt.Errorf("failed to save transaction %x: %v", tx.Hash().Bytes(), err)
		}
	}

	// 7. 保存转账索引（仅Type=0的交易）
	for _, tx := range block.Transactions {
		if tx.Type == 0 {
			if err := db.SaveTransfer(tx, block.Header.Height); err != nil {
				return fmt.Errorf("failed to save transfer index: %v", err)
			}
		}
	}

	return nil
}

// 【P2协议-Backfill专用】保存区块但不更新latest_height
// 用于向下同步历史区块时，避免latest_height被拉低
func (db *Database) SaveBlockForBackfill(block *core.Block) error {
	// 1. 序列化区块
	data, err := block.ToJSON()
	if err != nil {
		return err
	}

	// 2. 保存区块数据（key: b{hash} -> block）
	blockKey := makeBlockKey(block.Hash())
	if err := db.db.Put(blockKey, data, nil); err != nil {
		return err
	}

	// 3. 保存高度映射（key: h{height} -> hash）
	heightKey := makeHeightKey(block.Header.Height)
	hashBytes := block.Hash().Bytes()
	if err := db.db.Put(heightKey, hashBytes, nil); err != nil {
		return err
	}

	// 【关键】不更新latest_height，保持链头高度不变

	// 4. 保存时间戳索引
	db.SaveBlockTimestamp(block.Header.Height, block.Header.Timestamp)

	// 5. 保存区块内的所有交易到交易索引
	for _, tx := range block.Transactions {
		if err := db.SaveTransaction(tx); err != nil {
			return fmt.Errorf("failed to save transaction %x: %v", tx.Hash().Bytes(), err)
		}
	}

	// 6. 保存转账索引（仅Type=0的交易）
	for _, tx := range block.Transactions {
		if tx.Type == 0 {
			if err := db.SaveTransfer(tx, block.Header.Height); err != nil {
				return fmt.Errorf("failed to save transfer index: %v", err)
			}
		}
	}

	return nil
}

// 获取区块（通过哈希）
func (db *Database) GetBlock(hash core.Hash) (*core.Block, error) {
	key := makeBlockKey(hash)
	data, err := db.db.Get(key, nil)
	if err != nil {
		return nil, err
	}

	var block core.Block
	if err := block.FromJSON(data); err != nil {
		return nil, err
	}

	return &block, nil
}

// 获取区块（通过高度）
func (db *Database) GetBlockByHeight(height uint64) (*core.Block, error) {
	// 1. 获取哈希
	heightKey := makeHeightKey(height)
	hashBytes, err := db.db.Get(heightKey, nil)
	if err != nil {
		return nil, err
	}

	hash := core.BytesToHash(hashBytes)

	// 2. 获取区块
	return db.GetBlock(hash)
}

// 获取最新区块高度
func (db *Database) GetLatestHeight() (uint64, error) {
	data, err := db.db.Get([]byte("meta:latest_height"), nil)
	if err != nil {
		if err == leveldb.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}

	return binary.BigEndian.Uint64(data), nil
}

// 保存最新区块高度
func (db *Database) SaveLatestHeight(height uint64) error {
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, height)
	return db.db.Put([]byte("meta:latest_height"), data, nil)
}

// 获取最新区块
func (db *Database) GetLatestBlock() (*core.Block, error) {
	height, err := db.GetLatestHeight()
	if err != nil {
		return nil, err
	}

	if height == 0 {
		return nil, leveldb.ErrNotFound
	}

	return db.GetBlockByHeight(height)
}

// 获取区块范围（从fromHeight到toHeight，包含两端）
func (db *Database) GetBlockRange(fromHeight, toHeight uint64) ([]*core.Block, error) {
	// 参数验证：防止fromHeight > toHeight导致下溢
	if fromHeight > toHeight {
		return []*core.Block{}, nil
	}
	blocks := make([]*core.Block, 0, toHeight-fromHeight+1)

	for height := fromHeight; height <= toHeight; height++ {
		block, err := db.GetBlockByHeight(height)
		if err != nil {
			// 如果某个区块不存在，返回已获取的区块
			if err == leveldb.ErrNotFound {
				break
			}
			return nil, err
		}
		blocks = append(blocks, block)
	}

	return blocks, nil
}

// 【P2协议】获取最早区块高度（从高度1开始向上查找第一个存在的区块）
func (db *Database) GetEarliestHeight() uint64 {
	// 从高度1开始查找第一个存在的区块
	// 使用heightPrefix进行范围查询
	iter := db.db.NewIterator(util.BytesPrefix(heightPrefix), nil)
	defer iter.Release()

	if iter.First() {
		// 解析高度（key格式: h{8字节高度}）
		key := iter.Key()
		if len(key) == 9 { // 1字节前缀 + 8字节高度
			height := binary.BigEndian.Uint64(key[1:])
			return height
		}
	}

	return 1 // 默认返回1
}

// ========== 账户存储 ==========

// 保存账户
func (db *Database) SaveAccount(account *core.Account) error {
	data, err := account.ToJSON()
	if err != nil {
		return err
	}

	key := makeAccountKey(account.Address)
	return db.db.Put(key, data, nil)
}

// 获取账户
func (db *Database) GetAccount(address string) (*core.Account, error) {
	key := makeAccountKey(address)
	data, err := db.db.Get(key, nil)
	if err != nil {
		if err == leveldb.ErrNotFound {
			// 账户不存在，返回新账户
			return core.NewAccount(address), nil
		}
		return nil, err
	}

	var account core.Account
	if err := account.FromJSON(data); err != nil {
		return nil, err
	}

	return &account, nil
}

// 获取所有账户
func (db *Database) GetAllAccounts() ([]*core.Account, error) {
	accounts := []*core.Account{}

	iter := db.db.NewIterator(util.BytesPrefix(accountPrefix), nil)
	defer iter.Release()

	for iter.Next() {
		var account core.Account
		if err := account.FromJSON(iter.Value()); err != nil {
			continue
		}
		accounts = append(accounts, &account)
	}

	return accounts, iter.Error()
}

// ClearAllAccounts 清空所有账户（用于应用快照）
func (db *Database) ClearAllAccounts() error {
	iter := db.db.NewIterator(util.BytesPrefix(accountPrefix), nil)
	defer iter.Release()

	batch := new(leveldb.Batch)
	for iter.Next() {
		batch.Delete(iter.Key())
	}

	if err := iter.Error(); err != nil {
		return err
	}

	return db.db.Write(batch, nil)
}

// ========== 交易存储 ==========

// 保存交易
func (db *Database) SaveTransaction(tx *core.Transaction) error {
	data, err := tx.ToJSON()
	if err != nil {
		return err
	}

	key := makeTxKey(tx.Hash())
	return db.db.Put(key, data, nil)
}

// 获取交易
func (db *Database) GetTransaction(hash core.Hash) (*core.Transaction, error) {
	key := makeTxKey(hash)
	data, err := db.db.Get(key, nil)
	if err != nil {
		return nil, err
	}

	var tx core.Transaction
	if err := tx.FromJSON(data); err != nil {
		return nil, err
	}

	return &tx, nil
}

// ========== 辅助函数 ==========

func makeBlockKey(hash core.Hash) []byte {
	return append(blockPrefix, hash.Bytes()...)
}

func makeHeightKey(height uint64) []byte {
	key := make([]byte, 9)
	key[0] = heightPrefix[0]
	binary.BigEndian.PutUint64(key[1:], height)
	return key
}

func makeAccountKey(address string) []byte {
	return append(accountPrefix, []byte(address)...)
}

func makeTxKey(hash core.Hash) []byte {
	return append(txPrefix, hash.Bytes()...)
}
// 获取地址相关的交易（最近limit个）
func (db *Database) GetTransactionsByAddress(address string, limit int) ([]*core.Transaction, error) {
	if limit <= 0 {
		limit = 100 // 默认返回最近100笔交易
	}

	// 获取最新高度
	latestHeight, err := db.GetLatestHeight()
	if err != nil {
		return nil, err
	}

	// 计算查询范围（最近200个区块）
	fromHeight := uint64(1)
	if latestHeight > 200 {
		fromHeight = latestHeight - 200
	}

	// 获取区块范围
	blocks, err := db.GetBlockRange(fromHeight, latestHeight)
	if err != nil {
		return nil, err
	}

	// 收集相关交易（倒序遍历，最新的在前）
	transactions := make([]*core.Transaction, 0, limit)
	for i := len(blocks) - 1; i >= 0; i-- {
		block := blocks[i]
		for j := range block.Transactions {
			// 检查交易是否与地址相关（发送方或接收方）
			if block.Transactions[j].From == address || block.Transactions[j].To == address {
				transactions = append(transactions, block.Transactions[j])
				if len(transactions) >= limit {
					return transactions, nil
				}
			}
		}
	}

	return transactions, nil
}

// DeleteBlocksAboveHeight 删除指定高度以上的所有区块
// 用于链重组：删除错误的区块
func (db *Database) DeleteBlocksAboveHeight(targetHeight uint64) error {
	// 1. 获取当前最新高度
	latestHeight, err := db.GetLatestHeight()
	if err != nil {
		return err
	}

	if latestHeight <= targetHeight {
		// 无需删除
		return nil
	}

	fmt.Printf("⚠️  DELETE: Deleting blocks from height %d to %d\n", targetHeight+1, latestHeight)

	// 2. 删除每个高度的区块
	for height := targetHeight + 1; height <= latestHeight; height++ {
		// 获取该高度的哈希
		heightKey := makeHeightKey(height)
		hashBytes, err := db.db.Get(heightKey, nil)
		if err != nil {
			if err == leveldb.ErrNotFound {
				continue
			}
			return fmt.Errorf("failed to get hash for height %d: %v", height, err)
		}

		// 删除区块数据
		hash := core.BytesToHash(hashBytes)
		blockKey := makeBlockKey(hash)
		if err := db.db.Delete(blockKey, nil); err != nil {
			return fmt.Errorf("failed to delete block at height %d: %v", height, err)
		}

		// 删除高度映射
		if err := db.db.Delete(heightKey, nil); err != nil {
			return fmt.Errorf("failed to delete height mapping %d: %v", height, err)
		}

		fmt.Printf("Deleted block #%d\n", height)
	}

	// 3. 更新最新高度
	fmt.Printf("✓ DELETE: Deleted %d blocks\n", latestHeight-targetHeight)
	return db.SaveLatestHeight(targetHeight)
}

// ========== 转账索引存储 (遵循100天修剪规则) ==========

// TransferRecord 转账记录结构
type TransferRecord struct {
	TxHash      string `json:"tx_hash"`
	From        string `json:"from"`
	To          string `json:"to"`
	Amount      uint64 `json:"amount"`
	GasFee      uint64 `json:"gas_fee"`
	BlockHeight uint64 `json:"block_height"`
	Timestamp   int64  `json:"timestamp"`
	Nonce       uint64 `json:"nonce"`
}

// makeTransferKey 生成转账索引键
// 格式: x{8字节高度}{32字节txHash}
// 使用高度作为前缀，方便按时间顺序遍历和修剪
func makeTransferKey(height uint64, txHash core.Hash) []byte {
	key := make([]byte, 1+8+32)
	key[0] = transferPrefix[0]
	binary.BigEndian.PutUint64(key[1:9], height)
	copy(key[9:], txHash.Bytes())
	return key
}

// SaveTransfer 保存转账记录到索引
func (db *Database) SaveTransfer(tx *core.Transaction, blockHeight uint64) error {
	record := TransferRecord{
		TxHash:      tx.Hash().String(),
		From:        tx.From,
		To:          tx.To,
		Amount:      tx.Amount,
		GasFee:      tx.GasFee,
		BlockHeight: blockHeight,
		Timestamp:   tx.Timestamp,
		Nonce:       tx.Nonce,
	}

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	key := makeTransferKey(blockHeight, tx.Hash())
	return db.db.Put(key, data, nil)
}

// GetTransfers 获取转账列表（分页，按高度倒序）
// offset: 跳过的记录数
// limit: 返回的记录数
func (db *Database) GetTransfers(offset, limit int) ([]TransferRecord, int, error) {
	if limit <= 0 {
		limit = 20
	}

	// 使用反向迭代器，从最新到最旧
	iter := db.db.NewIterator(util.BytesPrefix(transferPrefix), nil)
	defer iter.Release()

	// 移动到末尾
	records := []TransferRecord{}
	total := 0

	// 先计算总数并收集所有记录
	allRecords := []TransferRecord{}
	for iter.Next() {
		var record TransferRecord
		if err := json.Unmarshal(iter.Value(), &record); err != nil {
			continue
		}
		allRecords = append(allRecords, record)
		total++
	}

	if err := iter.Error(); err != nil {
		return nil, 0, err
	}

	// 倒序（最新的在前）
	for i := len(allRecords) - 1; i >= 0; i-- {
		// 跳过offset条
		if offset > 0 {
			offset--
			continue
		}
		records = append(records, allRecords[i])
		if len(records) >= limit {
			break
		}
	}

	return records, total, nil
}

// GetTransfersByAddress 获取指定地址的转账记录
func (db *Database) GetTransfersByAddress(address string, offset, limit int) ([]TransferRecord, int, error) {
	if limit <= 0 {
		limit = 20
	}

	iter := db.db.NewIterator(util.BytesPrefix(transferPrefix), nil)
	defer iter.Release()

	// 收集相关记录
	allRecords := []TransferRecord{}
	for iter.Next() {
		var record TransferRecord
		if err := json.Unmarshal(iter.Value(), &record); err != nil {
			continue
		}
		// 筛选与地址相关的转账
		if record.From == address || record.To == address {
			allRecords = append(allRecords, record)
		}
	}

	if err := iter.Error(); err != nil {
		return nil, 0, err
	}

	total := len(allRecords)
	records := []TransferRecord{}

	// 倒序（最新的在前）
	for i := len(allRecords) - 1; i >= 0; i-- {
		if offset > 0 {
			offset--
			continue
		}
		records = append(records, allRecords[i])
		if len(records) >= limit {
			break
		}
	}

	return records, total, nil
}

// PruneTransfersBelowHeight 修剪指定高度以下的转账索引
// 遵循100天修剪规则
func (db *Database) PruneTransfersBelowHeight(minHeight uint64) (int, error) {
	iter := db.db.NewIterator(util.BytesPrefix(transferPrefix), nil)
	defer iter.Release()

	batch := new(leveldb.Batch)
	count := 0

	for iter.Next() {
		key := iter.Key()
		if len(key) < 9 {
			continue
		}
		// 解析高度
		height := binary.BigEndian.Uint64(key[1:9])
		if height < minHeight {
			batch.Delete(key)
			count++
		}
	}

	if err := iter.Error(); err != nil {
		return 0, err
	}

	if count > 0 {
		if err := db.db.Write(batch, nil); err != nil {
			return 0, err
		}
	}

	return count, nil
}

// GetTransferCount 获取转账索引总数
func (db *Database) GetTransferCount() (int, error) {
	iter := db.db.NewIterator(util.BytesPrefix(transferPrefix), nil)
	defer iter.Release()

	count := 0
	for iter.Next() {
		count++
	}

	return count, iter.Error()
}
