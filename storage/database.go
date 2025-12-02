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
// 主网存储架构：
// - blocks/       : Flat File区块数据
// - state/        : 36分片账户状态
// - checkpoints/  : Checkpoint文件
// - blockchain.db : 元数据+索引（不存区块和账户）
var (
	txPrefix       = []byte("t") // 交易索引
	transferPrefix = []byte("x") // 转账索引 (Type=0)
)

// Database 数据库
type Database struct {
	db         *leveldb.DB        // LevelDB：元数据、交易索引、转账索引
	stateStore *ShardedStateStore // 36分片：账户状态
	blockStore *BlockStore        // Flat File：区块数据
	dataDir    string
}

// OpenDatabase 打开数据库
func OpenDatabase(dataDir string) (*Database, error) {
	dbPath := filepath.Join(dataDir, "blockchain.db")
	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %v", err)
	}

	blockStore, err := NewBlockStore(dataDir)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create block store: %v", err)
	}

	stateStore, err := NewShardedStateStore(dataDir)
	if err != nil {
		db.Close()
		blockStore.Close()
		return nil, fmt.Errorf("failed to create sharded state store: %v", err)
	}

	return &Database{
		db:         db,
		stateStore: stateStore,
		blockStore: blockStore,
		dataDir:    dataDir,
	}, nil
}

// Close 关闭数据库
func (d *Database) Close() error {
	if d.stateStore != nil {
		d.stateStore.Close()
	}
	if d.blockStore != nil {
		d.blockStore.Close()
	}
	return d.db.Close()
}

// ========== 区块存储（Flat File） ==========

// SaveBlock 保存区块
func (d *Database) SaveBlock(block *core.Block) error {
	data, err := block.ToJSON()
	if err != nil {
		return err
	}

	if err := d.blockStore.WriteBlock(block.Header.Height, data); err != nil {
		return fmt.Errorf("failed to write block: %v", err)
	}

	if err := d.SaveLatestHeight(block.Header.Height); err != nil {
		return err
	}

	d.SaveBlockTimestamp(block.Header.Height, block.Header.Timestamp)

	for _, tx := range block.Transactions {
		if err := d.SaveTransaction(tx); err != nil {
			return fmt.Errorf("failed to save tx: %v", err)
		}
	}

	for _, tx := range block.Transactions {
		if tx.Type == 0 {
			d.SaveTransfer(tx, block.Header.Height)
		}
	}

	return nil
}

// SaveBlockForBackfill 保存区块但不更新latest_height
func (d *Database) SaveBlockForBackfill(block *core.Block) error {
	data, err := block.ToJSON()
	if err != nil {
		return err
	}

	if err := d.blockStore.WriteBlock(block.Header.Height, data); err != nil {
		return fmt.Errorf("failed to write block: %v", err)
	}

	d.SaveBlockTimestamp(block.Header.Height, block.Header.Timestamp)

	for _, tx := range block.Transactions {
		d.SaveTransaction(tx)
	}

	for _, tx := range block.Transactions {
		if tx.Type == 0 {
			d.SaveTransfer(tx, block.Header.Height)
		}
	}

	return nil
}

// GetBlockByHeight 获取区块
func (d *Database) GetBlockByHeight(height uint64) (*core.Block, error) {
	var data []byte
	var err error

	cfg := core.GetConsensusConfig()
	if cfg.StorageParams.VerifyBlockHash == 1 {
		data, err = d.blockStore.ReadBlockWithVerify(height)
	} else {
		data, err = d.blockStore.ReadBlock(height)
	}

	if err != nil {
		return nil, err
	}

	var block core.Block
	if err := block.FromJSON(data); err != nil {
		return nil, err
	}

	return &block, nil
}

// GetLatestHeight 获取最新区块高度
func (d *Database) GetLatestHeight() (uint64, error) {
	data, err := d.db.Get([]byte("meta:latest_height"), nil)
	if err != nil {
		if err == leveldb.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	return binary.BigEndian.Uint64(data), nil
}

// SaveLatestHeight 保存最新区块高度
func (d *Database) SaveLatestHeight(height uint64) error {
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, height)
	return d.db.Put([]byte("meta:latest_height"), data, nil)
}

// GetLatestBlock 获取最新区块
func (d *Database) GetLatestBlock() (*core.Block, error) {
	height, err := d.GetLatestHeight()
	if err != nil {
		return nil, err
	}
	if height == 0 {
		return nil, leveldb.ErrNotFound
	}
	return d.GetBlockByHeight(height)
}

// GetBlockRange 获取区块范围
func (d *Database) GetBlockRange(fromHeight, toHeight uint64) ([]*core.Block, error) {
	if fromHeight > toHeight {
		return []*core.Block{}, nil
	}

	blocks := make([]*core.Block, 0, toHeight-fromHeight+1)
	for height := fromHeight; height <= toHeight; height++ {
		block, err := d.GetBlockByHeight(height)
		if err != nil {
			if err == leveldb.ErrNotFound {
				break
			}
			return nil, err
		}
		blocks = append(blocks, block)
	}

	return blocks, nil
}

// GetEarliestHeight 获取最早区块高度
func (d *Database) GetEarliestHeight() uint64 {
	height, err := d.blockStore.GetEarliestBlockHeight()
	if err == nil && height > 0 {
		return height
	}
	return 1
}

// HasBlock 检查区块是否存在
func (d *Database) HasBlock(height uint64) bool {
	return d.blockStore.HasBlock(height)
}

// DeleteBlocksAboveHeight 删除指定高度以上的区块
func (d *Database) DeleteBlocksAboveHeight(targetHeight uint64) error {
	if err := d.blockStore.DeleteBlocksAboveHeight(targetHeight); err != nil {
		fmt.Printf("Flat file delete error: %v\n", err)
	}
	return d.SaveLatestHeight(targetHeight)
}

// PruneBlocks 剪枝旧区块
func (d *Database) PruneBlocks(keepBlocks uint64) (int, error) {
	latestHeight, err := d.GetLatestHeight()
	if err != nil {
		return 0, err
	}
	return d.blockStore.PruneChunks(latestHeight, keepBlocks)
}

// GetBlockStore 获取区块存储
func (d *Database) GetBlockStore() *BlockStore {
	return d.blockStore
}

// ========== 账户存储（36分片） ==========

// SaveAccount 保存账户
func (d *Database) SaveAccount(account *core.Account) error {
	return d.stateStore.SaveAccount(account)
}

// SaveAccountsBatch 批量保存账户
func (d *Database) SaveAccountsBatch(accounts []*core.Account) error {
	if len(accounts) == 0 {
		return nil
	}
	return d.stateStore.SaveAccountsBatch(accounts)
}

// GetAccount 获取账户
func (d *Database) GetAccount(address string) (*core.Account, error) {
	return d.stateStore.GetAccount(address)
}

// GetAllAccounts 获取所有账户
func (d *Database) GetAllAccounts() ([]*core.Account, error) {
	return d.stateStore.GetAllAccounts()
}

// ClearAllAccounts 清空所有账户
func (d *Database) ClearAllAccounts() error {
	return d.stateStore.ClearAllAccounts()
}

// GetShardStats 获取36分片统计
func (d *Database) GetShardStats() map[string]int {
	return d.stateStore.GetShardStats()
}

// GetStateStore 获取分片存储
func (d *Database) GetStateStore() *ShardedStateStore {
	return d.stateStore
}

// GetStateHeight 获取state对应的区块高度
func (d *Database) GetStateHeight() (uint64, error) {
	return d.stateStore.GetStateHeight()
}

// SaveAccountsBatchWithHeight 批量保存账户并更新高度
func (d *Database) SaveAccountsBatchWithHeight(accounts []*core.Account, height uint64) error {
	return d.stateStore.SaveAccountsBatchWithHeight(accounts, height)
}

// ========== 交易存储 ==========

func makeTxKey(hash core.Hash) []byte {
	return append(txPrefix, hash.Bytes()...)
}

// SaveTransaction 保存交易
func (d *Database) SaveTransaction(tx *core.Transaction) error {
	data, err := tx.ToJSON()
	if err != nil {
		return err
	}
	return d.db.Put(makeTxKey(tx.Hash()), data, nil)
}

// GetTransaction 获取交易
func (d *Database) GetTransaction(hash core.Hash) (*core.Transaction, error) {
	data, err := d.db.Get(makeTxKey(hash), nil)
	if err != nil {
		return nil, err
	}

	var tx core.Transaction
	if err := tx.FromJSON(data); err != nil {
		return nil, err
	}
	return &tx, nil
}

// GetTransactionsByAddress 获取地址相关交易
func (d *Database) GetTransactionsByAddress(address string, limit int) ([]*core.Transaction, error) {
	if limit <= 0 {
		limit = 100
	}

	latestHeight, err := d.GetLatestHeight()
	if err != nil {
		return nil, err
	}

	fromHeight := uint64(1)
	if latestHeight > 200 {
		fromHeight = latestHeight - 200
	}

	blocks, err := d.GetBlockRange(fromHeight, latestHeight)
	if err != nil {
		return nil, err
	}

	transactions := make([]*core.Transaction, 0, limit)
	for i := len(blocks) - 1; i >= 0; i-- {
		for _, tx := range blocks[i].Transactions {
			if tx.From == address || tx.To == address {
				transactions = append(transactions, tx)
				if len(transactions) >= limit {
					return transactions, nil
				}
			}
		}
	}

	return transactions, nil
}

// ========== 转账索引 ==========

// TransferRecord 转账记录
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

func makeTransferKey(height uint64, txHash core.Hash) []byte {
	key := make([]byte, 1+8+32)
	key[0] = transferPrefix[0]
	binary.BigEndian.PutUint64(key[1:9], height)
	copy(key[9:], txHash.Bytes())
	return key
}

// SaveTransfer 保存转账记录
func (d *Database) SaveTransfer(tx *core.Transaction, blockHeight uint64) error {
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

	return d.db.Put(makeTransferKey(blockHeight, tx.Hash()), data, nil)
}

// GetTransfers 获取转账列表
func (d *Database) GetTransfers(offset, limit int) ([]TransferRecord, int, error) {
	if limit <= 0 {
		limit = 20
	}

	iter := d.db.NewIterator(util.BytesPrefix(transferPrefix), nil)
	defer iter.Release()

	allRecords := []TransferRecord{}
	for iter.Next() {
		var record TransferRecord
		if err := json.Unmarshal(iter.Value(), &record); err != nil {
			continue
		}
		allRecords = append(allRecords, record)
	}

	if err := iter.Error(); err != nil {
		return nil, 0, err
	}

	total := len(allRecords)
	records := []TransferRecord{}

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

// GetTransfersByAddress 获取指定地址的转账记录
func (d *Database) GetTransfersByAddress(address string, offset, limit int) ([]TransferRecord, int, error) {
	if limit <= 0 {
		limit = 20
	}

	iter := d.db.NewIterator(util.BytesPrefix(transferPrefix), nil)
	defer iter.Release()

	allRecords := []TransferRecord{}
	for iter.Next() {
		var record TransferRecord
		if err := json.Unmarshal(iter.Value(), &record); err != nil {
			continue
		}
		if record.From == address || record.To == address {
			allRecords = append(allRecords, record)
		}
	}

	if err := iter.Error(); err != nil {
		return nil, 0, err
	}

	total := len(allRecords)
	records := []TransferRecord{}

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

// PruneTransfersBelowHeight 修剪旧转账索引
func (d *Database) PruneTransfersBelowHeight(minHeight uint64) (int, error) {
	iter := d.db.NewIterator(util.BytesPrefix(transferPrefix), nil)
	defer iter.Release()

	batch := new(leveldb.Batch)
	count := 0

	for iter.Next() {
		key := iter.Key()
		if len(key) < 9 {
			continue
		}
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
		if err := d.db.Write(batch, nil); err != nil {
			return 0, err
		}
	}

	return count, nil
}

// GetTransferCount 获取转账索引总数
func (d *Database) GetTransferCount() (int, error) {
	iter := d.db.NewIterator(util.BytesPrefix(transferPrefix), nil)
	defer iter.Release()

	count := 0
	for iter.Next() {
		count++
	}
	return count, iter.Error()
}
