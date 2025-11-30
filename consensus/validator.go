package consensus

import (
	"fmt"
	"log"
	"sort"
	"time"

	"fan-chain/core"
	"fan-chain/state"
	"fan-chain/storage"
	"golang.org/x/crypto/sha3"
)

// 验证者集合
type ValidatorSet struct {
	validators       []*core.Validator
	activeValidators []*core.Validator
	lastUpdate       time.Time
}

// 创建验证者集合
func NewValidatorSet() *ValidatorSet {
	return &ValidatorSet{
		validators:       make([]*core.Validator, 0),
		activeValidators: make([]*core.Validator, 0),
		lastUpdate:       time.Now(),
	}
}

// 从状态加载验证者
func (vs *ValidatorSet) LoadFromState(db *storage.Database) error {
	accounts, err := db.GetAllAccounts()
	if err != nil {
		return err
	}

	vs.validators = make([]*core.Validator, 0)

	for _, acc := range accounts {
		if acc.IsValidator() {
			validator := &core.Validator{
				Address:       acc.Address,
				StakedAmount:  acc.StakedBalance,
				Status:        core.ValActive,
				LastBlockTime: time.Now().Unix(),
				LastHeartbeat: time.Now().Unix(),
			}
			vs.validators = append(vs.validators, validator)
		}
	}

	return nil
}

// LoadFromCheckpoint 从checkpoint恢复验证者集合（确保VRF计算一致性）
func (vs *ValidatorSet) LoadFromCheckpoint(validators []core.ValidatorSnapshot) {
	vs.validators = make([]*core.Validator, 0)
	vs.activeValidators = make([]*core.Validator, 0)

	for _, snapshot := range validators {
		validator := &core.Validator{
			Address:       snapshot.Address,
			StakedAmount:  snapshot.Stake,
			VRFPublicKey:  snapshot.VRFPubKey,
			Status:        core.ValActive,
			LastBlockTime: time.Now().Unix(),
			LastHeartbeat: time.Now().Unix(),
		}
		vs.validators = append(vs.validators, validator)
		vs.activeValidators = append(vs.activeValidators, validator)
	}

	vs.lastUpdate = time.Now()
}

// 更新活跃验证者集
func (vs *ValidatorSet) UpdateActiveSet() {
	active := make([]*core.Validator, 0)
	for _, v := range vs.validators {
		if v.IsActive() {
			active = append(active, v)
		}
	}

	sort.Slice(active, func(i, j int) bool {
		if active[i].StakedAmount != active[j].StakedAmount {
			return active[i].StakedAmount > active[j].StakedAmount
		}
		return active[i].Address < active[j].Address
	})

	activeSetSize := core.ActiveValidatorSet()
	if len(active) > activeSetSize {
		vs.activeValidators = active[:activeSetSize]
	} else {
		vs.activeValidators = active
	}

	vs.lastUpdate = time.Now()
}

func (vs *ValidatorSet) GetActiveValidators() []*core.Validator {
	return vs.activeValidators
}

func (vs *ValidatorSet) IsActiveValidator(address string) bool {
	for _, v := range vs.activeValidators {
		if v.Address == address {
			return true
		}
	}
	return false
}

func (vs *ValidatorSet) AddValidator(validator *core.Validator) {
	// 检查是否已存在
	for _, v := range vs.validators {
		if v.Address == validator.Address {
			// 更新质押金额
			v.StakedAmount = validator.StakedAmount
			vs.UpdateActiveSet()
			log.Printf("📊 验证者 %s 质押更新: %d", validator.Address[:10], validator.StakedAmount)
			return
		}
	}
	vs.validators = append(vs.validators, validator)
	vs.UpdateActiveSet()
	log.Printf("📊 Current validator set: %d validators", len(vs.activeValidators))
}

// RemoveValidator 移除验证者
func (vs *ValidatorSet) RemoveValidator(address string) {
	newValidators := make([]*core.Validator, 0)
	for _, v := range vs.validators {
		if v.Address != address {
			newValidators = append(newValidators, v)
		}
	}
	vs.validators = newValidators
	vs.UpdateActiveSet()
	log.Printf("📊 验证者 %s 已移除, 当前验证者数: %d", address[:10], len(vs.activeValidators))
}

// 共识引擎
// 共识引擎
type ConsensusEngine struct {
	validatorSet     *ValidatorSet
	stateManager     *state.StateManager
	nodeAddress      string
	nodePrivateKey   []byte
	nodePublicKey    []byte
	getOnlinePeersFn func() []string
}

func NewConsensusEngine(sm *state.StateManager) *ConsensusEngine {
	return &ConsensusEngine{
		validatorSet: NewValidatorSet(),
		stateManager: sm,
	}
}

func (ce *ConsensusEngine) ValidatorSet() *ValidatorSet {
	return ce.validatorSet
}

func (ce *ConsensusEngine) SetNodeKeys(address string, privateKey, publicKey []byte) {
	ce.nodeAddress = address
	ce.nodePrivateKey = privateKey
	ce.nodePublicKey = publicKey
}

func (ce *ConsensusEngine) SetOnlinePeersFunction(fn func() []string) {
	ce.getOnlinePeersFn = fn
}

// SelectProposer 【P2协议】VRF预计算选择出块者
// 核心原则：
// 1. VRF出块顺序在Checkpoint前一块（Block N-1）预计算
// 2. 种子基于Checkpoint区块哈希，整个周期内顺序固定
// 3. 等概率轮询，不看质押量
func (ce *ConsensusEngine) SelectProposer(height uint64, prevBlockHash core.Hash) (string, error) {
	activeVals := ce.validatorSet.GetActiveValidators()
	if len(activeVals) == 0 {
		return "", fmt.Errorf("no active validators")
	}

	sortedValidators := make([]*core.Validator, len(activeVals))
	copy(sortedValidators, activeVals)
	sort.Slice(sortedValidators, func(i, j int) bool {
		return sortedValidators[i].Address < sortedValidators[j].Address
	})

	// 【P2协议】计算当前区块所属的Checkpoint周期
	checkpointInterval := core.GetConsensusConfig().BlockParams.CheckpointInterval
	if checkpointInterval == 0 {
		checkpointInterval = 5
	}

	// 计算当前区块在周期内的偏移量（0-4）
	var cycleOffset uint64
	if height == 0 {
		cycleOffset = 0
	} else {
		cycleOffset = (height - 1) % checkpointInterval
	}

	// 计算Checkpoint周期的起始高度
	cycleStart := height - cycleOffset

	// 【P2协议核心】种子 = 前一个Checkpoint哈希 + 周期起始高度
	seed := append(prevBlockHash.Bytes(), core.Uint64ToBytes(cycleStart)...)

	// 为周期内每个位置生成确定性的proposer
	positionSeed := append(seed, core.Uint64ToBytes(cycleOffset)...)
	randomSeed := sha3.Sum256(positionSeed)

	var randomValue uint64
	for i := 0; i < 8; i++ {
		randomValue = (randomValue << 8) | uint64(randomSeed[i])
	}

	validatorCount := uint64(len(sortedValidators))
	selectedIndex := randomValue % validatorCount

	return sortedValidators[selectedIndex].Address, nil
}

// GetCycleProposers 【P2协议】获取整个Checkpoint周期的出块顺序
func (ce *ConsensusEngine) GetCycleProposers(cycleStartHeight uint64, seedHash core.Hash) ([]string, error) {
	activeVals := ce.validatorSet.GetActiveValidators()
	if len(activeVals) == 0 {
		return nil, fmt.Errorf("no active validators")
	}

	sortedValidators := make([]*core.Validator, len(activeVals))
	copy(sortedValidators, activeVals)
	sort.Slice(sortedValidators, func(i, j int) bool {
		return sortedValidators[i].Address < sortedValidators[j].Address
	})

	checkpointInterval := core.GetConsensusConfig().BlockParams.CheckpointInterval
	if checkpointInterval == 0 {
		checkpointInterval = 5
	}

	proposers := make([]string, checkpointInterval)
	baseSeed := append(seedHash.Bytes(), core.Uint64ToBytes(cycleStartHeight)...)

	for i := uint64(0); i < checkpointInterval; i++ {
		positionSeed := append(baseSeed, core.Uint64ToBytes(i)...)
		randomSeed := sha3.Sum256(positionSeed)

		var randomValue uint64
		for j := 0; j < 8; j++ {
			randomValue = (randomValue << 8) | uint64(randomSeed[j])
		}

		validatorCount := uint64(len(sortedValidators))
		selectedIndex := randomValue % validatorCount
		proposers[i] = sortedValidators[selectedIndex].Address
	}

	return proposers, nil
}

func (ce *ConsensusEngine) filterOnlineValidators(validators []*core.Validator) []*core.Validator {
	if ce.getOnlinePeersFn == nil {
		return validators
	}

	onlinePeers := ce.getOnlinePeersFn()
	onlineMap := make(map[string]bool)
	for _, addr := range onlinePeers {
		onlineMap[addr] = true
	}

	online := make([]*core.Validator, 0)
	for _, v := range validators {
		if v.Address == ce.nodeAddress || onlineMap[v.Address] {
			online = append(online, v)
		}
	}

	return online
}

// VerifyProposer 验证提案者是否为活跃验证者
func (ce *ConsensusEngine) VerifyProposer(block *core.Block, prevBlockHash core.Hash) error {
	if !ce.validatorSet.IsActiveValidator(block.Header.Proposer) {
		return fmt.Errorf("proposer %s is not an active validator", block.Header.Proposer)
	}
	return nil
}

func (ce *ConsensusEngine) CreateRewardTransactions(proposer string, validators []*core.Validator) []*core.Transaction {
	txs := make([]*core.Transaction, 0)

	genesisAccount, err := ce.stateManager.GetAccount(core.GenesisAddress)
	if err != nil {
		blockRewardTx := core.NewRewardTx(proposer, core.BlockReward())
		txs = append(txs, blockRewardTx)
		return txs
	}

	consensusConfig := core.GetConsensusConfig()
	currentReward := consensusConfig.CalculateBlockReward(genesisAccount.AvailableBalance)

	if currentReward == 0 {
		return txs
	}

	blockRewardTx := core.NewRewardTx(proposer, currentReward)
	txs = append(txs, blockRewardTx)

	return txs
}

func (ce *ConsensusEngine) CalculateStateRoot() core.Hash {
	return core.Hash{}
}
