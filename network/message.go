package network

import (
	"encoding/json"
	"fan-chain/core"
)

// 消息类型
type MessageType uint8

const (
	MsgPing          MessageType = 0  // Ping
	MsgPong          MessageType = 1  // Pong
	MsgGetBlocks     MessageType = 2  // 请求区块
	MsgBlocks        MessageType = 3  // 区块数据
	MsgGetLatest     MessageType = 4  // 请求最新区块高度
	MsgLatestHeight  MessageType = 5  // 最新区块高度
	MsgNewBlock      MessageType = 6  // 新区块广播
	MsgTransaction   MessageType = 7  // 交易广播
	MsgKeyExchange   MessageType = 8  // 密钥交换
	MsgEncrypted     MessageType = 9  // 加密消息
	MsgGetCheckpoint     MessageType = 10 // 请求最新checkpoint
	MsgCheckpoint        MessageType = 11 // checkpoint数据
	MsgGetState          MessageType = 12 // 请求状态快照
	MsgStateData         MessageType = 13 // 状态快照数据
	MsgGetEarliestHeight MessageType = 14 // 【P2协议】请求最早区块高度
	MsgEarliestHeight    MessageType = 15 // 【P2协议】最早区块高度响应
)

// 消息结构
type Message struct {
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// Ping消息
type PingMessage struct {
	Address             string `json:"address"`              // 节点地址
	Height              uint64 `json:"height"`               // 当前高度
	LatestBlockHash     string `json:"latest_block_hash"`    // 最新区块哈希（用于分叉检测）
	CheckpointHeight    uint64 `json:"checkpoint_height"`    // 最新checkpoint高度
	CheckpointHash      string `json:"checkpoint_hash"`      // 最新checkpoint区块哈希
	CheckpointTimestamp int64  `json:"checkpoint_timestamp"` // 最新checkpoint时间戳（用于分叉选择）
	ConsensusVersion    string `json:"consensus_version"`    // 共识版本
	ConsensusHash       string `json:"consensus_hash"`       // 共识哈希
}

// Pong消息
type PongMessage struct {
	Address             string `json:"address"`              // 节点地址
	Height              uint64 `json:"height"`               // 当前高度
	LatestBlockHash     string `json:"latest_block_hash"`    // 最新区块哈希（用于分叉检测）
	CheckpointHeight    uint64 `json:"checkpoint_height"`    // 最新checkpoint高度
	CheckpointHash      string `json:"checkpoint_hash"`      // 最新checkpoint区块哈希
	CheckpointTimestamp int64  `json:"checkpoint_timestamp"` // 最新checkpoint时间戳（用于分叉选择）
	ConsensusVersion    string `json:"consensus_version"`    // 共识版本
	ConsensusHash       string `json:"consensus_hash"`       // 共识哈希
}

// 请求区块消息
type GetBlocksMessage struct {
	FromHeight uint64 `json:"from_height"` // 起始高度
	ToHeight   uint64 `json:"to_height"`   // 结束高度
}

// 区块数据消息
type BlocksMessage struct {
	Blocks []*core.Block `json:"blocks"`
}

// 最新高度消息
type LatestHeightMessage struct {
	Height uint64 `json:"height"`
}

// 新区块广播消息
type NewBlockMessage struct {
	Block *core.Block `json:"block"`
}

// 交易广播消息
type TransactionMessage struct {
	Transaction *core.Transaction `json:"transaction"`
}

// 请求checkpoint消息
type GetCheckpointMessage struct {
	Count uint64 `json:"count"` // 请求最新N个checkpoint（默认3）
}

// checkpoint数据消息（现在可以包含多个checkpoint）
type CheckpointMessage struct {
	Checkpoints []CheckpointInfo `json:"checkpoints"` // checkpoint列表（从新到旧）
}

// 单个checkpoint信息
type CheckpointInfo struct {
	Checkpoint     *core.Checkpoint `json:"checkpoint"`      // checkpoint元数据
	HasStateData   bool             `json:"has_state_data"`  // 是否包含状态数据
	CompressedSize uint64           `json:"compressed_size"` // 压缩后大小
}

// 请求状态快照消息
type GetStateMessage struct {
	Height uint64 `json:"height"` // 请求指定高度的状态快照
}

// 状态快照数据消息
type StateDataMessage struct {
	Height         uint64 `json:"height"`          // 快照高度
	CompressedData []byte `json:"compressed_data"` // 压缩后的状态数据
}

// 【P2协议】请求最早区块高度消息
type GetEarliestHeightMessage struct {
}

// 【P2协议】最早区块高度响应消息
type EarliestHeightMessage struct {
	Height uint64 `json:"height"` // 本节点最早的区块高度
}

// 创建消息
func NewMessage(msgType MessageType, payload interface{}) (*Message, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return &Message{
		Type:    msgType,
		Payload: data,
	}, nil
}

// 解析消息
func (m *Message) ParsePayload(v interface{}) error {
	return json.Unmarshal(m.Payload, v)
}

// 序列化消息
func (m *Message) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

// 反序列化消息
func UnmarshalMessage(data []byte) (*Message, error) {
	var msg Message
	err := json.Unmarshal(data, &msg)
	return &msg, err
}
