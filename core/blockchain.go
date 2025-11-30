package core

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// 区块链
type Blockchain struct {
	latestBlock  *Block
	latestHeight uint64
	mu           sync.RWMutex
}

// 创建区块链
func NewBlockchain() *Blockchain {
	return &Blockchain{
		latestBlock:  nil,
		latestHeight: 0,
	}
}

// 初始化（加载或创建创世区块）
func (bc *Blockchain) Initialize(genesisBlock *Block) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	bc.latestBlock = genesisBlock
	bc.latestHeight = genesisBlock.Header.Height

	log.Printf("Blockchain initialized at height %d", bc.latestHeight)
}

// 获取最新区块
func (bc *Blockchain) GetLatestBlock() *Block {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.latestBlock
}

// SetLatestBlock 直接设置最新区块（用于Ephemeral checkpoint恢复）
func (bc *Blockchain) SetLatestBlock(block *Block) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.latestBlock = block
	bc.latestHeight = block.Header.Height
	log.Printf("📌 【Ephemeral】Directly set blockchain to height %d", bc.latestHeight)
}

// SetLatestBlockWithHash 设置最新区块并覆盖其hash（用于checkpoint占位区块）
func (bc *Blockchain) SetLatestBlockWithHash(block *Block, correctHash Hash) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// 创建一个特殊的占位区块，保持正确的hash
	placeholderBlock := &Block{
		Header: block.Header,
		Transactions: block.Transactions,
		// 注意：这个区块的Hash()方法会返回错误的值
		// 但我们通过特殊标记来处理这种情况
	}

	// 添加标记表明这是checkpoint占位区块
	placeholderBlock.IsCheckpointPlaceholder = true
	placeholderBlock.CheckpointHash = correctHash

	bc.latestBlock = placeholderBlock
	bc.latestHeight = block.Header.Height
	log.Printf("📌 【Ephemeral】Set placeholder block at height %d with checkpoint hash %x",
		bc.latestHeight, correctHash.Bytes()[:8])
}

// 获取最新高度
func (bc *Blockchain) GetLatestHeight() uint64 {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.latestHeight
}

// 添加区块
func (bc *Blockchain) AddBlock(block *Block) error {
	return bc.AddBlockWithOptions(block, false)
}

// AddBlockWithOptions 添加区块（带选项）
// skipTimestampCheck: true表示跳过时间戳验证（用于同步历史区块）
func (bc *Blockchain) AddBlockWithOptions(block *Block, skipTimestampCheck bool) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// 验证区块
	var err error
	if skipTimestampCheck {
		err = block.ValidateWithOptions(bc.latestBlock, true)
	} else {
		err = block.Validate(bc.latestBlock)
	}
	if err != nil {
		return fmt.Errorf("invalid block: %v", err)
	}

	// 更新链状态
	bc.latestBlock = block
	bc.latestHeight = block.Header.Height

	h := block.Hash()
	log.Printf("Block #%d added: %s", block.Header.Height, h.String())
	return nil
}

// 验证新区块
func (bc *Blockchain) ValidateBlock(block *Block) error {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	return block.Validate(bc.latestBlock)
}

// 等待下一个出块时间
func (bc *Blockchain) WaitForNextBlock() {
	if bc.latestBlock == nil {
		return
	}

	nextBlockTime := time.Unix(bc.latestBlock.Header.Timestamp, 0).Add(time.Duration(BlockInterval()) * time.Second)
	waitDuration := time.Until(nextBlockTime)

	if waitDuration > 0 {
		time.Sleep(waitDuration)
	}
}

// RollbackToHeight 回滚区块链到指定高度
// 用于链重组：当检测到本地链上有错误的区块时，回滚到正确的高度
func (bc *Blockchain) RollbackToHeight(targetHeight uint64, targetBlock *Block) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if targetHeight > bc.latestHeight {
		return fmt.Errorf("cannot rollback to higher height: current=%d, target=%d", bc.latestHeight, targetHeight)
	}

	if targetHeight == bc.latestHeight {
		// 已经在目标高度，无需回滚
		return nil
	}

	log.Printf("⚠️  ROLLBACK: Rolling back blockchain from height %d to %d", bc.latestHeight, targetHeight)
	bc.latestBlock = targetBlock
	bc.latestHeight = targetHeight
	log.Printf("✓ ROLLBACK: Blockchain rolled back to height %d", targetHeight)
	return nil
}

// JumpToBlock 快速跳转到指定区块（用于快速同步）
// 用于新节点快速同步：跳过历史区块，直接从指定高度开始同步
// 注意：这个方法只更新内存中的链状态，不保存区块到数据库
// 调用者需要确保目标区块的前一个区块已经存在于数据库中，以避免REORG失败
func (bc *Blockchain) JumpToBlock(block *Block) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	log.Printf("⚡ FAST SYNC: Jumping from height %d to %d (skipping %d blocks)",
		bc.latestHeight, block.Header.Height-1, block.Header.Height-bc.latestHeight-1)
	bc.latestBlock = block
	bc.latestHeight = block.Header.Height - 1 // 设置为区块的前一个高度，这样AddBlock就能正常工作
	return nil
}

// SetLatestHeightForSync 直接设置区块链高度（用于历史/归档节点从checkpoint开始同步）
// 创建一个占位区块以便同步逻辑正常工作
func (bc *Blockchain) SetLatestHeightForSync(height uint64) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// 创建占位区块，仅包含高度信息
	placeholderBlock := &Block{
		Header: &BlockHeader{
			Height:    height,
			Timestamp: time.Now().Unix(),
		},
		Transactions:            []*Transaction{},
		IsCheckpointPlaceholder: true,
	}

	bc.latestBlock = placeholderBlock
	bc.latestHeight = height
	log.Printf("📌 【Archive Sync】Set blockchain height to %d for sync start", height)
}

// AddBlockForSync 添加区块（用于历史/归档节点同步，放宽验证）
// 仅验证区块高度是当前高度+1，不验证前置区块hash（因为可能还没同步到）
func (bc *Blockchain) AddBlockForSync(block *Block) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	expectedHeight := bc.latestHeight + 1
	if block.Header.Height != expectedHeight {
		return fmt.Errorf("invalid height: expected %d, got %d", expectedHeight, block.Header.Height)
	}

	// 更新链状态
	bc.latestBlock = block
	bc.latestHeight = block.Header.Height

	h := block.Hash()
	log.Printf("Block #%d synced: %s", block.Header.Height, h.String())
	return nil
}
