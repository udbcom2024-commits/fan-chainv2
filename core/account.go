package core

import (
	"encoding/json"
	"fmt"
)

// 节点类型
type NodeType uint8

const (
	NodeRegular   NodeType = 0 // 普通用户
	NodeValidator NodeType = 1 // 主网验证者
	NodeLight     NodeType = 2 // 轻节点
	NodeProxy     NodeType = 3 // 机场节点
)

// 验证者状态
type ValidatorStatus uint8

const (
	ValSyncing   ValidatorStatus = 0 // 同步中（禁止出块）
	ValActive    ValidatorStatus = 1 // 活跃
	ValSuspended ValidatorStatus = 2 // 暂停
	ValSlashed   ValidatorStatus = 3 // 已惩罚
	ValExited    ValidatorStatus = 4 // 已退出
)

// 账户状态
type Account struct {
	Address          string `json:"address"`
	AvailableBalance uint64 `json:"available_balance"`
	StakedBalance    uint64 `json:"staked_balance"`
	Nonce            uint64 `json:"nonce"`

	// 节点身份
	NodeType   NodeType `json:"node_type"`
	NodeStatus string   `json:"node_status,omitempty"`

	// 验证者特有
	StakeLockedUntil int64 `json:"stake_locked_until,omitempty"`

	// 未来扩展
	CodeHash    Hash `json:"code_hash,omitempty"`
	StorageRoot Hash `json:"storage_root,omitempty"`
}

// 创建新账户
func NewAccount(address string) *Account {
	return &Account{
		Address:          address,
		AvailableBalance: 0,
		StakedBalance:    0,
		Nonce:            0,
		NodeType:         NodeRegular,
	}
}

// 总余额
func (a *Account) TotalBalance() uint64 {
	return a.AvailableBalance + a.StakedBalance
}

// 增加余额
func (a *Account) AddBalance(amount uint64) {
	a.AvailableBalance += amount
}

// 减少余额
func (a *Account) SubBalance(amount uint64) error {
	if a.AvailableBalance < amount {
		return fmt.Errorf("insufficient balance: have %d, want %d",
			a.AvailableBalance, amount)
	}
	a.AvailableBalance -= amount
	return nil
}

// 抵押
func (a *Account) Stake(amount uint64) error {
	if a.AvailableBalance < amount {
		return fmt.Errorf("insufficient balance for staking")
	}

	a.AvailableBalance -= amount
	a.StakedBalance += amount
	a.NodeType = NodeValidator

	return nil
}

// 取消抵押
func (a *Account) Unstake(amount uint64) error {
	if a.StakedBalance < amount {
		return fmt.Errorf("insufficient staked balance")
	}

	a.StakedBalance -= amount
	a.AvailableBalance += amount

	if a.StakedBalance == 0 {
		a.NodeType = NodeRegular
	}

	return nil
}

// 是否是验证者
func (a *Account) IsValidator() bool {
	return a.NodeType == NodeValidator &&
		a.StakedBalance >= ValidatorStakeRequired()
}

// JSON序列化
func (a *Account) ToJSON() ([]byte, error) {
	return json.Marshal(a)
}

func (a *Account) FromJSON(data []byte) error {
	return json.Unmarshal(data, a)
}

// 字符串表示
func (a *Account) String() string {
	return fmt.Sprintf("Account{%s: %d FAN (available:%d, staked:%d), nonce:%d}",
		a.Address,
		a.TotalBalance()/FANUnit(),
		a.AvailableBalance/FANUnit(),
		a.StakedBalance/FANUnit(),
		a.Nonce)
}

// 验证者信息
type Validator struct {
	Address      string          `json:"address"`
	StakedAmount uint64          `json:"staked_amount"`
	Status       ValidatorStatus `json:"status"`

	// VRF密钥
	VRFPublicKey  []byte `json:"vrf_public_key,omitempty"`
	VRFPrivateKey []byte `json:"vrf_private_key,omitempty"`

	// 统计信息
	LastBlockTime int64  `json:"last_block_time"`
	LastHeartbeat int64  `json:"last_heartbeat"`
	TotalBlocks   uint64 `json:"total_blocks"`
	MissedBlocks  uint64 `json:"missed_blocks"`
}

// 创建验证者
func NewValidator(address string, stakedAmount uint64) *Validator {
	return &Validator{
		Address:       address,
		StakedAmount:  stakedAmount,
		Status:        ValActive,
		LastBlockTime: CurrentTimestamp(),
		LastHeartbeat: CurrentTimestamp(),
		TotalBlocks:   0,
		MissedBlocks:  0,
	}
}

// 是否活跃
func (v *Validator) IsActive() bool {
	return v.Status == ValActive &&
		v.StakedAmount >= ValidatorStakeRequired()
}

// 字符串表示
func (v *Validator) String() string {
	return fmt.Sprintf("Validator{%s: staked %d FAN, blocks:%d, status:%s}",
		v.Address,
		v.StakedAmount/FANUnit(),
		v.TotalBlocks,
		v.StatusString())
}

func (v *Validator) StatusString() string {
	switch v.Status {
	case ValActive:
		return "Active"
	case ValSuspended:
		return "Suspended"
	case ValSlashed:
		return "Slashed"
	case ValExited:
		return "Exited"
	default:
		return "Unknown"
	}
}
