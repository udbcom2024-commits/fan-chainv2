package core

import (
	"encoding/json"
	"log"
	"os"
	"sync"
)

// ⚠️ DEPRECATED: This file is kept for backward compatibility only.
// Reward calculation has been integrated into consensus_config.go
// Use ConsensusConfig.CalculateBlockReward() instead of GetRewardManager().CalculateBlockReward()
// The rewards.json file is no longer used - all reward parameters are now in consensus.json

// 奖励配置
type RewardConfig struct {
	BaseBlockReward uint64              `json:"base_block_reward"` // 基础出块奖励
	GenesisAddress  string              `json:"genesis_address"`   // 创世地址
	MinRewardUnit   uint64              `json:"min_reward_unit"`   // 最小奖励单位
	Thresholds      []RewardThreshold   `json:"thresholds"`        // 奖励阈值列表
}

// 奖励阈值
type RewardThreshold struct {
	Balance     uint64 `json:"balance"`     // 余额阈值
	Description string `json:"description"` // 描述(仅用于可读性)
}

// 奖励管理器
type RewardManager struct {
	config      *RewardConfig
	configMutex sync.RWMutex
	configPath  string
}

var (
	rewardManager     *RewardManager
	rewardManagerOnce sync.Once
)

// 获取奖励管理器单例
func GetRewardManager() *RewardManager {
	rewardManagerOnce.Do(func() {
		rewardManager = &RewardManager{
			configPath: "./rewards.json",
		}
		if err := rewardManager.LoadConfig(); err != nil {
			log.Printf("警告: 加载奖励配置失败,使用默认值: %v", err)
			rewardManager.setDefaultConfig()
		}
	})
	return rewardManager
}

// 设置默认配置
func (rm *RewardManager) setDefaultConfig() {
	rm.config = &RewardConfig{
		BaseBlockReward: 10000000, // 10 FAN
		GenesisAddress:  GenesisAddress,
		MinRewardUnit:   1,
		Thresholds:      []RewardThreshold{},
	}
}

// 加载配置
func (rm *RewardManager) LoadConfig() error {
	rm.configMutex.Lock()
	defer rm.configMutex.Unlock()

	data, err := os.ReadFile(rm.configPath)
	if err != nil {
		return err
	}

	config := &RewardConfig{}
	if err := json.Unmarshal(data, config); err != nil {
		return err
	}

	rm.config = config
	log.Printf("奖励配置加载成功: 基础奖励=%d, 阈值数=%d", config.BaseBlockReward, len(config.Thresholds))
	return nil
}

// 计算当前区块奖励
// 参数: genesisBalance - 创世地址当前可用余额
// 返回: 实际奖励金额 (最小单位)
func (rm *RewardManager) CalculateBlockReward(genesisBalance uint64) uint64 {
	rm.configMutex.RLock()
	defer rm.configMutex.RUnlock()

	// 如果没有配置阈值,返回基础奖励
	if len(rm.config.Thresholds) == 0 {
		return rm.config.BaseBlockReward
	}

	// 定义关键阈值常量
	const (
		Threshold_0_1_Yi = 10000000000000  // 0.1亿 FAN (最小单位)
		Threshold_0_2_Yi = 20000000000000  // 0.2亿 FAN (最小单位)
	)

	// 特殊区间1: 余额 < 0.1亿, 取消所有奖励
	if genesisBalance < Threshold_0_1_Yi {
		return 0
	}

	// 特殊区间2: 0.1亿 <= 余额 < 0.2亿, 固定最小单位奖励
	if genesisBalance >= Threshold_0_1_Yi && genesisBalance < Threshold_0_2_Yi {
		return 1 // 1最小单位 = 0.000001 FAN
	}

	// 正常区间: 余额 >= 0.2亿, 按阈值减半计算
	// 计算跨越了多少个阈值 (每跨越一个阈值,奖励减半一次)
	halvingCount := 0
	for _, threshold := range rm.config.Thresholds {
		if genesisBalance < threshold.Balance {
			halvingCount++
		}
	}

	// 计算实际奖励: 基础奖励 × (0.5 ^ halvingCount)
	reward := rm.config.BaseBlockReward
	for i := 0; i < halvingCount; i++ {
		reward = reward / 2
	}

	// 如果奖励小于最小单位,返回最小单位 (而不是0)
	if reward < rm.config.MinRewardUnit {
		return rm.config.MinRewardUnit
	}

	return reward
}

// 获取当前奖励状态(用于日志和监控)
func (rm *RewardManager) GetRewardStatus(genesisBalance uint64) map[string]interface{} {
	rm.configMutex.RLock()
	defer rm.configMutex.RUnlock()

	currentReward := rm.CalculateBlockReward(genesisBalance)

	// 找到当前所在的阈值区间
	currentThresholdIndex := -1
	for i, threshold := range rm.config.Thresholds {
		if genesisBalance < threshold.Balance {
			currentThresholdIndex = i
			break
		}
	}

	status := map[string]interface{}{
		"genesis_balance":  genesisBalance,
		"current_reward":   currentReward,
		"base_reward":      rm.config.BaseBlockReward,
		"threshold_index":  currentThresholdIndex,
	}

	if currentThresholdIndex >= 0 && currentThresholdIndex < len(rm.config.Thresholds) {
		status["current_threshold"] = rm.config.Thresholds[currentThresholdIndex].Description
	}

	return status
}
