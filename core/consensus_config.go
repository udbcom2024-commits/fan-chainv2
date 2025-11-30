package core

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	"golang.org/x/crypto/sha3"
)

// 共识配置 - 所有节点必须一致的参数
type ConsensusConfig struct {
	// 共识版本和哈希
	ConsensusVersion string `json:"consensus_version"` // 共识版本号
	ConsensusHash    string `json:"consensus_hash"`    // 共识参数哈希（自动计算，用于快速校验）

	// 链参数
	ChainParams ChainParams `json:"chain_params"`

	// 区块参数
	BlockParams BlockParams `json:"block_params"`

	// 经济参数
	EconomicParams EconomicParams `json:"economic_params"`

	// 验证者参数
	ValidatorParams ValidatorParams `json:"validator_params"`

	// 交易参数
	TransactionParams TransactionParams `json:"transaction_params"`

	// 网络参数
	NetworkParams NetworkParams `json:"network_params"`

	// 安全参数
	SecurityParams SecurityParams `json:"security_params"`

	// 存储参数
	StorageParams StorageParams `json:"storage_params"`

	// 奖励阈值（从rewards.json合并过来）
	RewardThresholds []RewardThreshold `json:"reward_thresholds"`
}

// 链参数
type ChainParams struct {
	// TotalSupply 硬编码，不从配置读取（保证总量永不改变）
	FANUnit        uint64 `json:"fan_unit"`         // 1 FAN = 多少最小单位
	FANDecimals    int    `json:"fan_decimals"`     // 代币精度
	GenesisAddress string `json:"genesis_address"`  // 创世地址
	GenesisTimestamp int64 `json:"genesis_timestamp"` // 创世区块时间戳
}

// 硬编码的总供应量 - 永不改变
const TotalSupplyHardcoded uint64 = 1400000000000000 // 14亿FAN

// 区块参数
type BlockParams struct {
	BlockIntervalSeconds      int    `json:"block_interval_seconds"`        // 出块间隔（秒）
	FinalityBlocks            int    `json:"finality_blocks"`               // 确认需要的区块数
	CheckpointInterval        uint64 `json:"checkpoint_interval"`           // Checkpoint生成间隔（区块数）
	CheckpointKeepCount       int    `json:"checkpoint_keep_count"`         // 保留的checkpoint数量
	MaxTimestampDrift         int64  `json:"max_timestamp_drift"`           // 最大时间戳偏移（秒）
	MaxBlockSize              uint64 `json:"max_block_size"`                // 区块最大大小（字节）
	BlockDataThresholdPercent int    `json:"block_data_threshold_percent"` // Data字段阈值百分比（0-100）
}

// 经济参数
type EconomicParams struct {
	MinGasFee              uint64 `json:"min_gas_fee"`               // 最小手续费
	MaxGasFee              uint64 `json:"max_gas_fee"`               // 最大手续费
	BaseBlockReward        uint64 `json:"base_block_reward"`         // 基础出块奖励
	MinRewardUnit          uint64 `json:"min_reward_unit"`           // 最小奖励单位
	ValidatorStakeRequired uint64 `json:"validator_stake_required"`  // 验证者最低质押
}

// 验证者参数
type ValidatorParams struct {
	MaxValidators               int `json:"max_validators"`                 // 最大验证者数量
	ActiveValidatorSet          int `json:"active_validator_set"`           // 活跃验证者集合大小
	CheckpointActivationBuffer  int `json:"checkpoint_activation_buffer"`   // checkpoint激活缓冲（距下次checkpoint少于此块数时等待）
}

// 存储参数
type StorageParams struct {
	LedgerRetentionDays        int  `json:"ledger_retention_days"`         // 账本保留天数
	AutoCleanupEnabled         bool `json:"auto_cleanup_enabled"`          // 自动清理开关
	CleanupCheckIntervalHours  int  `json:"cleanup_check_interval_hours"`  // 清理检查间隔(小时)
	MinBlocksToKeep            int  `json:"min_blocks_to_keep"`            // 最少保留区块数
}

// 交易参数
type TransactionParams struct {
	MaxTxSize         uint64 `json:"max_tx_size"`          // 最大交易大小(字节)
	MaxTxPerBlock     uint64 `json:"max_tx_per_block"`     // 每块最大交易数
	MinTransferAmount uint64 `json:"min_transfer_amount"`  // 最小转账金额
	MaxDataSize       uint64 `json:"max_data_size"`        // Data字段最大长度(字节)
	MemoMaxLength     uint64 `json:"memo_max_length"`      // 备注最大长度(字节)
}

// 网络参数
type NetworkParams struct {
	MaxPeers               int `json:"max_peers"`                 // 最大连接节点数
	MinPeers               int `json:"min_peers"`                 // 最小连接节点数
	PeerHandshakeTimeout   int `json:"peer_handshake_timeout"`    // 握手超时(秒)
	SyncBatchSize          int `json:"sync_batch_size"`           // 同步批次大小(区块数)
	MaxBlockRequestSize    int `json:"max_block_request_size"`    // 单次请求最大区块数
	BroadcastRetryInterval int `json:"broadcast_retry_interval"`  // 广播重试间隔(秒)
	PingInterval           int `json:"ping_interval"`             // 心跳间隔(秒)
}

// 安全参数
type SecurityParams struct {
	MaxReorgDepth    int   `json:"max_reorg_depth"`     // 最大重组深度
	MinBlockTimeMs   int64 `json:"min_block_time_ms"`   // 最小出块时间(毫秒,含抖动)
	MaxBlockTimeMs   int64 `json:"max_block_time_ms"`   // 最大出块时间(毫秒,含抖动)
	DoubleSignSlash  int   `json:"double_sign_slash"`   // 双签惩罚百分比(0-100)
	OfflineSlashBlks int   `json:"offline_slash_blocks"`// 离线多少块后惩罚
}

// 共识配置管理器
type ConsensusConfigManager struct {
	config     *ConsensusConfig
	configPath string
	mu         sync.RWMutex
}

var (
	consensusManager     *ConsensusConfigManager
	consensusManagerOnce sync.Once
)

// GetConsensusConfig 获取共识配置单例
func GetConsensusConfig() *ConsensusConfig {
	consensusManagerOnce.Do(func() {
		consensusManager = &ConsensusConfigManager{
			configPath: "./consensus.json",
		}
		if err := consensusManager.Load(); err != nil {
			log.Printf("⚠️  加载consensus.json失败，使用默认配置: %v", err)
			consensusManager.setDefault()
		}
	})

	consensusManager.mu.RLock()
	defer consensusManager.mu.RUnlock()
	return consensusManager.config
}

// 设置默认配置
func (m *ConsensusConfigManager) setDefault() {
	m.config = &ConsensusConfig{
		ConsensusVersion: "1.0.0",
		ConsensusHash:    "", // 自动计算
		ChainParams: ChainParams{
			FANUnit:          1000000,           // 1 FAN = 1000000 最小单位
			FANDecimals:      6,
			GenesisAddress:   "F25gxrj3tppc07hunne7hztvde5gkaw78f3xa",
			GenesisTimestamp: 1700000000, // 2023-11-14 22:13:20 UTC
		},
		BlockParams: BlockParams{
			BlockIntervalSeconds:      5,
			FinalityBlocks:            8,
			CheckpointInterval:        5,
			CheckpointKeepCount:       3,
			MaxTimestampDrift:         300,     // 5分钟
			MaxBlockSize:              1048576, // 1MB
			BlockDataThresholdPercent: 80,      // 80%
		},
		EconomicParams: EconomicParams{
			MinGasFee:              1,
			MaxGasFee:              10,
			BaseBlockReward:        10000000, // 10 FAN
			MinRewardUnit:          1,
			ValidatorStakeRequired: 1000000000000, // 1M FAN
		},
		ValidatorParams: ValidatorParams{
			MaxValidators:              100,
			ActiveValidatorSet:         14,
			CheckpointActivationBuffer: 3, // 距下次checkpoint少于3块时等待
		},
		TransactionParams: TransactionParams{
			MaxTxSize:         10240, // 10KB
			MaxTxPerBlock:     1000,
			MinTransferAmount: 1,
			MaxDataSize:       1024, // 1KB
			MemoMaxLength:     256,
		},
		NetworkParams: NetworkParams{
			MaxPeers:               50,
			MinPeers:               1,
			PeerHandshakeTimeout:   30,
			SyncBatchSize:          100,
			MaxBlockRequestSize:    1000,
			BroadcastRetryInterval: 6,
			PingInterval:           60,
		},
		SecurityParams: SecurityParams{
			MaxReorgDepth:    100,
			MinBlockTimeMs:   4500, // 4.5秒
			MaxBlockTimeMs:   5500, // 5.5秒
			DoubleSignSlash:  100,  // 100%没收
			OfflineSlashBlks: 1000,
		},
		StorageParams: StorageParams{
			LedgerRetentionDays:       100,
			AutoCleanupEnabled:        true,
			CleanupCheckIntervalHours: 24,
			MinBlocksToKeep:           10000,
		},
		RewardThresholds: []RewardThreshold{},
	}
}

// Load 加载配置文件
func (m *ConsensusConfigManager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return err
	}

	config := &ConsensusConfig{}
	if err := json.Unmarshal(data, config); err != nil {
		return err
	}

	// 计算共识哈希
	config.ConsensusHash = m.calculateConsensusHash(config)

	m.config = config
	log.Printf("✅ 共识配置加载成功")
	log.Printf("   版本: %s", config.ConsensusVersion)
	log.Printf("   哈希: %s", config.ConsensusHash[:16]+"...")
	log.Printf("   出块间隔: %ds", config.BlockParams.BlockIntervalSeconds)
	log.Printf("   Checkpoint间隔: %d块", config.BlockParams.CheckpointInterval)
	log.Printf("   账本保留: %d天", config.StorageParams.LedgerRetentionDays)

	return nil
}

// calculateConsensusHash 计算共识参数的哈希值
func (m *ConsensusConfigManager) calculateConsensusHash(config *ConsensusConfig) string {
	// 构建确定性的字符串表示（排除consensus_hash本身）
	hashInput := fmt.Sprintf(
		"v:%s|ts:%d|unit:%d|dec:%d|bi:%d|ci:%d|ckc:%d|mtd:%d|mbs:%d|bdtp:%d|mgf:%d|xgf:%d|br:%d|mru:%d|vsr:%d|mv:%d|avs:%d|"+
		"txs:%d|txpb:%d|mta:%d|mds:%d|mml:%d|"+
		"mp:%d|mnp:%d|pht:%d|sbs:%d|mbr:%d|bri:%d|pi:%d|"+
		"mrd:%d|mbtm:%d|xbtm:%d|dss:%d|osb:%d|"+
		"lrd:%d|ac:%t|cci:%d|mbk:%d|thr:%d",
		config.ConsensusVersion,
		TotalSupplyHardcoded, // 硬编码的总供应量
		config.ChainParams.FANUnit,
		config.ChainParams.FANDecimals,
		config.BlockParams.BlockIntervalSeconds,
		config.BlockParams.CheckpointInterval,
		config.BlockParams.CheckpointKeepCount,
		config.BlockParams.MaxTimestampDrift,
		config.BlockParams.MaxBlockSize,
		config.BlockParams.BlockDataThresholdPercent,
		config.EconomicParams.MinGasFee,
		config.EconomicParams.MaxGasFee,
		config.EconomicParams.BaseBlockReward,
		config.EconomicParams.MinRewardUnit,
		config.EconomicParams.ValidatorStakeRequired,
		config.ValidatorParams.MaxValidators,
		config.ValidatorParams.ActiveValidatorSet,
		// 交易参数
		config.TransactionParams.MaxTxSize,
		config.TransactionParams.MaxTxPerBlock,
		config.TransactionParams.MinTransferAmount,
		config.TransactionParams.MaxDataSize,
		config.TransactionParams.MemoMaxLength,
		// 网络参数
		config.NetworkParams.MaxPeers,
		config.NetworkParams.MinPeers,
		config.NetworkParams.PeerHandshakeTimeout,
		config.NetworkParams.SyncBatchSize,
		config.NetworkParams.MaxBlockRequestSize,
		config.NetworkParams.BroadcastRetryInterval,
		config.NetworkParams.PingInterval,
		// 安全参数
		config.SecurityParams.MaxReorgDepth,
		config.SecurityParams.MinBlockTimeMs,
		config.SecurityParams.MaxBlockTimeMs,
		config.SecurityParams.DoubleSignSlash,
		config.SecurityParams.OfflineSlashBlks,
		// 存储参数
		config.StorageParams.LedgerRetentionDays,
		config.StorageParams.AutoCleanupEnabled,
		config.StorageParams.CleanupCheckIntervalHours,
		config.StorageParams.MinBlocksToKeep,
		len(config.RewardThresholds),
	)

	// 包含奖励阈值
	for _, threshold := range config.RewardThresholds {
		hashInput += fmt.Sprintf("|%d", threshold.Balance)
	}

	// 计算SHA3-256哈希
	hash := sha3.Sum256([]byte(hashInput))
	return hex.EncodeToString(hash[:])
}

// Save 保存配置文件
func (m *ConsensusConfigManager) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := json.MarshalIndent(m.config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.configPath, data, 0644)
}

// CalculateBlockReward 计算区块奖励（整合原reward.go的逻辑）
func (c *ConsensusConfig) CalculateBlockReward(genesisBalance uint64) uint64 {
	// 定义关键阈值常量
	const (
		Threshold_0_1_Yi = 10000000000000  // 0.1亿 FAN
		Threshold_0_2_Yi = 20000000000000  // 0.2亿 FAN
	)

	// 特殊区间1: 余额 < 0.1亿，取消所有奖励
	if genesisBalance < Threshold_0_1_Yi {
		return 0
	}

	// 特殊区间2: 0.1亿 <= 余额 < 0.2亿，固定最小单位奖励
	if genesisBalance >= Threshold_0_1_Yi && genesisBalance < Threshold_0_2_Yi {
		return c.EconomicParams.MinRewardUnit
	}

	// 正常区间: 余额 >= 0.2亿，按阈值减半计算
	halvingCount := 0
	for _, threshold := range c.RewardThresholds {
		if genesisBalance < threshold.Balance {
			halvingCount++
		}
	}

	// 计算实际奖励: 基础奖励 × (0.5 ^ halvingCount)
	reward := c.EconomicParams.BaseBlockReward
	for i := 0; i < halvingCount; i++ {
		reward = reward / 2
	}

	// 如果奖励小于最小单位，返回最小单位
	if reward < c.EconomicParams.MinRewardUnit {
		return c.EconomicParams.MinRewardUnit
	}

	return reward
}
