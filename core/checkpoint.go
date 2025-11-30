package core

import (
	"encoding/json"
	"fmt"

	"fan-chain/crypto"
)

// ValidatorSnapshot 验证者快照（用于VRF计算一致性）
// 方案1：精简存储，只保留VRF必需的32字节
type ValidatorSnapshot struct {
	Address    string `json:"addr"`     // 验证者地址 (39字节)
	Stake      uint64 `json:"stake"`    // 质押量（命格权重） (8字节)
	VRFPubKey  []byte `json:"vrf_key"`  // VRF公钥精简版 (32字节，状态极简主义)
}

// Checkpoint 检查点结构
type Checkpoint struct {
	Height       uint64               `json:"height"`       // 检查点高度
	BlockHash    Hash                 `json:"block_hash"`   // 该高度区块哈希
	PreviousHash Hash                 `json:"prev_hash"`    // 前一个区块哈希（长期方案）
	StateRoot    Hash                 `json:"state_root"`   // 状态树根哈希
	Timestamp    int64                `json:"timestamp"`    // 时间戳
	Proposer     string               `json:"proposer"`     // 提议者地址
	Validators   []ValidatorSnapshot  `json:"validators"`   // 验证者快照（新增）
	Signature    []byte               `json:"signature"`    // 提议者签名
}

// NewCheckpoint 创建新检查点
func NewCheckpoint(height uint64, blockHash Hash, previousHash Hash, stateRoot Hash, timestamp int64, proposer string) *Checkpoint {
	return &Checkpoint{
		Height:       height,
		BlockHash:    blockHash,
		PreviousHash: previousHash,
		StateRoot:    stateRoot,
		Timestamp:    timestamp,
		Proposer:     proposer,
	}
}

// Hash 计算检查点哈希
func (cp *Checkpoint) Hash() Hash {
	// 包含验证者信息的哈希，确保验证者集合的完整性
	validatorsData := ""
	for _, v := range cp.Validators {
		validatorsData += fmt.Sprintf("%s:%d:%x,", v.Address, v.Stake, v.VRFPubKey)
	}

	data := fmt.Sprintf("%d:%s:%s:%s:%d:%s:%s",
		cp.Height,
		cp.BlockHash.String(),
		cp.PreviousHash.String(),
		cp.StateRoot.String(),
		cp.Timestamp,
		cp.Proposer,
		validatorsData,
	)
	return CalculateHash([]byte(data))
}

// Serialize 序列化检查点
func (cp *Checkpoint) Serialize() ([]byte, error) {
	return json.MarshalIndent(cp, "", "  ")
}

// DeserializeCheckpoint 反序列化检查点
func DeserializeCheckpoint(data []byte) (*Checkpoint, error) {
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

// Verify 验证检查点签名
func (cp *Checkpoint) Verify(publicKey []byte) error {
	if len(cp.Signature) == 0 {
		return fmt.Errorf("checkpoint not signed")
	}

	// 计算消息哈希
	msgHash := cp.Hash()

	// 验证签名
	if !crypto.Verify(publicKey, msgHash.Bytes(), cp.Signature) {
		return fmt.Errorf("invalid checkpoint signature")
	}

	return nil
}

// Sign 签名检查点
func (cp *Checkpoint) Sign(privateKey []byte) error {
	msgHash := cp.Hash()
	sig, err := crypto.Sign(privateKey, msgHash.Bytes())
	if err != nil {
		return err
	}
	cp.Signature = sig
	return nil
}