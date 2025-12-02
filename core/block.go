package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// 区块头
type BlockHeader struct {
	Height       uint64 `json:"height"`
	PreviousHash Hash   `json:"previous_hash"`
	Timestamp    int64  `json:"timestamp"`
	StateRoot    Hash   `json:"state_root"`
	TxRoot       Hash   `json:"tx_root"`
	Proposer     string `json:"proposer"` // 出块者地址

	// VRF共识
	VRFProof  []byte `json:"vrf_proof"`
	VRFOutput []byte `json:"vrf_output"`

	// 签名
	Signature []byte `json:"signature"`
}

// 区块
type Block struct {
	Header       *BlockHeader   `json:"header"`
	Transactions []*Transaction `json:"transactions"`
	Data         []byte         `json:"data,omitempty"` // 通用加密数据字段（机场链接、公告等）

	// 缓存的哈希
	hash *Hash

	// 【Ephemeral】checkpoint占位区块标记
	IsCheckpointPlaceholder bool `json:"-"`
	CheckpointHash          Hash `json:"-"`
}

// 创建新区块
func NewBlock(height uint64, prevHash Hash, proposer string, txs []*Transaction) *Block {
	header := &BlockHeader{
		Height:       height,
		PreviousHash: prevHash,
		Timestamp:    CurrentTimestamp(),
		Proposer:     proposer,
	}

	block := &Block{
		Header:       header,
		Transactions: txs,
	}

	// 计算Merkle根
	block.Header.TxRoot = block.CalculateTxRoot()

	return block
}

// 计算区块哈希
func (b *Block) Hash() Hash {
	// 【Ephemeral】如果是checkpoint占位区块，返回checkpoint记录的正确hash
	if b.IsCheckpointPlaceholder {
		return b.CheckpointHash
	}

	if b.hash != nil {
		return *b.hash
	}

	data := b.Header.Bytes()
	h := CalculateHash(data)
	b.hash = &h
	return h
}

// 区块头字节表示
func (h *BlockHeader) Bytes() []byte {
	buf := new(bytes.Buffer)

	buf.Write(Uint64ToBytes(h.Height))
	buf.Write(h.PreviousHash.Bytes())
	buf.Write(Uint64ToBytes(uint64(h.Timestamp)))
	buf.Write(h.StateRoot.Bytes())
	buf.Write(h.TxRoot.Bytes())
	buf.WriteString(h.Proposer)

	if len(h.VRFProof) > 0 {
		buf.Write(h.VRFProof)
	}
	if len(h.VRFOutput) > 0 {
		buf.Write(h.VRFOutput)
	}

	return buf.Bytes()
}

// 签名数据（不包含签名本身）
func (h *BlockHeader) SignData() []byte {
	buf := new(bytes.Buffer)

	buf.Write(Uint64ToBytes(h.Height))
	buf.Write(h.PreviousHash.Bytes())
	buf.Write(Uint64ToBytes(uint64(h.Timestamp)))
	buf.Write(h.StateRoot.Bytes())
	buf.Write(h.TxRoot.Bytes())
	buf.WriteString(h.Proposer)

	if len(h.VRFProof) > 0 {
		buf.Write(h.VRFProof)
	}
	if len(h.VRFOutput) > 0 {
		buf.Write(h.VRFOutput)
	}

	return buf.Bytes()
}

// 计算交易Merkle根（简化版，直接哈希所有交易）
func (b *Block) CalculateTxRoot() Hash {
	if len(b.Transactions) == 0 {
		return Hash{}
	}

	buf := new(bytes.Buffer)
	for _, tx := range b.Transactions {
		txHash := tx.Hash()
		buf.Write(txHash.Bytes())
	}

	return CalculateHash(buf.Bytes())
}

// 验证区块
func (b *Block) Validate(prevBlock *Block) error {
	return b.ValidateWithOptions(prevBlock, false)
}

// 验证区块（带选项）
func (b *Block) ValidateWithOptions(prevBlock *Block, skipTimestampCheck bool) error {
	// 1. 检查高度
	if prevBlock != nil {
		if b.Header.Height != prevBlock.Header.Height+1 {
			return fmt.Errorf("invalid height: expected %d, got %d",
				prevBlock.Header.Height+1, b.Header.Height)
		}

		// 2. 检查前一区块哈希
		// 【Ephemeral】如果前一区块是checkpoint占位区块，跳过PrevHash检查
		// 因为占位区块的hash是从checkpoint恢复的，可能与实际计算不匹配
		if !prevBlock.IsCheckpointPlaceholder && b.Header.PreviousHash != prevBlock.Hash() {
			return fmt.Errorf("invalid previous hash")
		}
	}

	// 3. 检查时间戳
	if prevBlock != nil {
		if b.Header.Timestamp <= prevBlock.Header.Timestamp {
			return fmt.Errorf("invalid timestamp")
		}

	// 3.1. 检查时间戳不能太超前（防止时间戳攻击）
	// 同步模式下跳过未来时间检查
	if !skipTimestampCheck {
		maxFutureTime := time.Now().UnixMilli() + 60000 // 允许60秒的时间偏差（毫秒级）
		if b.Header.Timestamp > maxFutureTime {
			return fmt.Errorf("invalid timestamp: too far in future (block: %d, max: %d)",
				b.Header.Timestamp, maxFutureTime)
		}
	}
	}

	// 4. 验证交易根
	txRoot := b.CalculateTxRoot()
	if txRoot != b.Header.TxRoot {
		return fmt.Errorf("invalid tx root")
	}

	// 5. 验证所有交易
	for i, tx := range b.Transactions {
		if err := tx.Validate(skipTimestampCheck); err != nil {
			return fmt.Errorf("invalid transaction %d: %v", i, err)
		}
	}

	return nil
}

// JSON序列化
func (b *Block) ToJSON() ([]byte, error) {
	return json.MarshalIndent(b, "", "  ")
}

func (b *Block) FromJSON(data []byte) error {
	return json.Unmarshal(data, b)
}

// 字符串表示
func (b *Block) String() string {
	h := b.Hash()
	return fmt.Sprintf("Block{Height:%d, Hash:%x, Txs:%d, Proposer:%s}",
		b.Header.Height,
		h.Bytes(),
		len(b.Transactions),
		b.Header.Proposer)
}

// 创建创世区块
// 设计原则：只有创世地址（F25gxrj3tppc07hunne7hztvde5gkaw78f3xa）应当硬编码
// 所有其他地址必须通过配置文件或交易动态添加
func CreateGenesisBlock() *Block {
	// 创世交易：给创世地址分配全部代币（14亿FAN）
	genesisTx := &Transaction{
		Type:      TxReward,
		From:      "",
		To:        GenesisAddress,
		Amount:    TotalSupply, // 1400000000000000 最小单位
		GasFee:    0,
		Nonce:     0,
		Timestamp: GenesisTimestamp(),
	}

	block := &Block{
		Header: &BlockHeader{
			Height:       0,
			PreviousHash: Hash{},
			Timestamp:    GenesisTimestamp(),
			Proposer:     GenesisAddress,
		},
		Transactions: []*Transaction{genesisTx},
	}

	block.Header.TxRoot = block.CalculateTxRoot()
	return block
}
