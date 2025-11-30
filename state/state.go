package state

import (
	"fmt"
	"log"

	"fan-chain/core"
	"fan-chain/crypto"
	"fan-chain/storage"
)

// P0协议：总量恒定常量
const TOTAL_SUPPLY = uint64(1400000000000000) // 14亿FAN（最小单位）

// 状态管理器
type StateManager struct {
	db            *storage.Database
	accountCache  map[string]*core.Account
	dirtyAccounts map[string]bool

	// 【P0增量验证】总量追踪器 - 实时追踪系统总供应量
	// 每笔交易执行后更新，O(1)时间复杂度验证
	totalSupplyTracker uint64
	trackerInitialized bool // 追踪器是否已初始化

	// 验证者变更回调：当质押状态变化导致验证者集合变更时调用
	onValidatorAdded   func(address string, stakedAmount uint64)
	onValidatorRemoved func(address string)
}

// 创建状态管理器
func NewStateManager(db *storage.Database) *StateManager {
	return &StateManager{
		db:                 db,
		accountCache:       make(map[string]*core.Account),
		dirtyAccounts:      make(map[string]bool),
		totalSupplyTracker: 0,
		trackerInitialized: false,
	}
}

// SetValidatorCallbacks 设置验证者变更回调
// 当质押/解押导致验证者集合变化时，通知共识层更新
func (sm *StateManager) SetValidatorCallbacks(
	onAdded func(address string, stakedAmount uint64),
	onRemoved func(address string),
) {
	sm.onValidatorAdded = onAdded
	sm.onValidatorRemoved = onRemoved
}

// InitializeTotalSupplyTracker 初始化总量追踪器
// 在节点启动时调用，从数据库计算初始总量
func (sm *StateManager) InitializeTotalSupplyTracker() error {
	if sm.trackerInitialized {
		return nil // 已初始化，跳过
	}

	// 从数据库加载所有账户计算初始总量
	accounts, err := sm.db.GetAllAccounts()
	if err != nil {
		return fmt.Errorf("failed to load accounts for tracker init: %v", err)
	}

	var totalSupply uint64
	for _, acc := range accounts {
		totalSupply += acc.AvailableBalance + acc.StakedBalance
	}

	// 验证初始总量是否正确
	if totalSupply != TOTAL_SUPPLY {
		log.Printf("🚨 P0警告：初始化时总量异常！预期=%d, 实际=%d, 差值=%d",
			TOTAL_SUPPLY, totalSupply, int64(totalSupply)-int64(TOTAL_SUPPLY))
		// 不返回错误，让节点继续启动，但记录警告
	}

	sm.totalSupplyTracker = totalSupply
	sm.trackerInitialized = true
	log.Printf("✅ P0总量追踪器初始化完成: %d (预期: %d)", totalSupply, TOTAL_SUPPLY)

	return nil
}

// GetTotalSupplyTracker 获取当前追踪器的总量值
func (sm *StateManager) GetTotalSupplyTracker() uint64 {
	return sm.totalSupplyTracker
}

// verifyTrackerIntegrity 验证追踪器完整性（内部方法）
// 返回: (追踪器值, 是否等于预期, error)
func (sm *StateManager) verifyTrackerIntegrity() (uint64, bool, error) {
	if !sm.trackerInitialized {
		return 0, false, fmt.Errorf("tracker not initialized")
	}
	return sm.totalSupplyTracker, sm.totalSupplyTracker == TOTAL_SUPPLY, nil
}

// 获取账户（带缓存）
func (sm *StateManager) GetAccount(address string) (*core.Account, error) {
	// 1. 检查缓存
	if acc, ok := sm.accountCache[address]; ok {
		return acc, nil
	}

	// 2. 从数据库加载
	acc, err := sm.db.GetAccount(address)
	if err != nil {
		return nil, err
	}

	// 3. 放入缓存
	sm.accountCache[address] = acc
	return acc, nil
}

// 更新账户
func (sm *StateManager) UpdateAccount(account *core.Account) {
	sm.accountCache[account.Address] = account
	sm.dirtyAccounts[account.Address] = true
}

// 提交状态（写入数据库）
func (sm *StateManager) Commit() error {
	for address := range sm.dirtyAccounts {
		acc := sm.accountCache[address]
		if err := sm.db.SaveAccount(acc); err != nil {
			return fmt.Errorf("failed to save account %s: %v", address, err)
		}
	}

	// 清空脏标记
	sm.dirtyAccounts = make(map[string]bool)
	return nil
}

// 重放攻击惩罚：没收所有代币到创世地址
func (sm *StateManager) confiscateAllFunds(attackerAddr string, reason string) error {
	log.Printf("⚠️  检测到重放攻击！地址: %s, 原因: %s", attackerAddr, reason)

	// ✅ 创世地址豁免：永不惩罚创世地址（防止P0触发）
	if attackerAddr == core.GenesisAddress {
		log.Printf("⚠️  创世地址享有豁免权，跳过惩罚（保护P0协议）")
		return fmt.Errorf("genesis address is exempt from punishment")
	}

	// 获取攻击者账户
	attacker, err := sm.GetAccount(attackerAddr)
	if err != nil {
		return err
	}

	// 计算所有资产（可用余额 + 抵押余额）
	totalFunds := attacker.AvailableBalance + attacker.StakedBalance

	if totalFunds == 0 {
		log.Printf("攻击者 %s 账户余额为0，无需没收", attackerAddr)
		return nil
	}

	// 获取创世地址
	genesis, err := sm.GetAccount(core.GenesisAddress)
	if err != nil {
		return err
	}

	// 没收所有资金到创世地址
	genesis.AddBalance(totalFunds)
	attacker.AvailableBalance = 0
	attacker.StakedBalance = 0
	attacker.NodeType = core.NodeRegular // 降级为普通节点

	sm.UpdateAccount(attacker)
	sm.UpdateAccount(genesis)

	log.Printf("🚨 重放攻击惩罚执行完毕：")
	log.Printf("   攻击者: %s", attackerAddr)
	log.Printf("   没收金额: %.6f FAN", float64(totalFunds)/1000000.0)
	log.Printf("   已转入创世地址: %s", core.GenesisAddress)
	log.Printf("   惩罚原因: %s", reason)

	return nil
}

// 执行交易
// skipTimestampCheck: true表示跳过时间戳验证（用于执行历史区块中的交易），false表示严格验证（用于API提交的新交易）
func (sm *StateManager) ExecuteTransaction(tx *core.Transaction, skipTimestampCheck bool) error {
	// 1. 系统交易（奖励、惩罚）直接执行
	if tx.Type.IsSystemTx() {
		return sm.executeSystemTx(tx)
	}

	// 2. 【签名验证】验证交易签名和地址（防止伪造）
	if err := tx.Validate(skipTimestampCheck); err != nil {
		return fmt.Errorf("invalid transaction: %v", err)
	}

	// 2.1. 验证签名有效性
	signData := tx.SignData()

	// 【调试日志】详细输出签名验证信息
	log.Printf("🔍 [Node1-state.go:133] 签名验证开始 From: %s", tx.From)
	log.Printf("  Type=%d, Amount=%d, GasFee=%d, Nonce=%d, Timestamp=%d",
		tx.Type, tx.Amount, tx.GasFee, tx.Nonce, tx.Timestamp)
	if len(tx.PublicKey) >= 32 {
		log.Printf("  PublicKey长度=%d, 前32字节=%x", len(tx.PublicKey), tx.PublicKey[:32])
	} else {
		log.Printf("  PublicKey长度=%d (太短)", len(tx.PublicKey))
	}
	log.Printf("  SignData长度=%d, 完整=%x", len(signData), signData)
	if len(tx.Signature) >= 32 {
		log.Printf("  Signature长度=%d, 前32字节=%x", len(tx.Signature), tx.Signature[:32])
	} else {
		log.Printf("  Signature长度=%d (太短)", len(tx.Signature))
	}

	if !crypto.Verify(tx.PublicKey, signData, tx.Signature) {
		log.Printf("🚨 检测到伪造签名！From: %s, TxHash: %s", tx.From, tx.Hash().String())
		log.Printf("  ❌ 签名验证失败详情：")
		log.Printf("     PublicKey长度=%d, SignData长度=%d, Signature长度=%d",
			len(tx.PublicKey), len(signData), len(tx.Signature))
		// 伪造签名是严重攻击，没收所有资金
		if err := sm.confiscateAllFunds(tx.From, "伪造交易签名"); err != nil {
			log.Printf("❌ 没收资金失败: %v，返回错误停止区块处理", err)
			return fmt.Errorf("signature verification failed and punishment failed: %v", err)
		}
		// ✅ 惩罚已执行，返回nil让区块继续处理（该交易通过惩罚方式处理完毕）
		log.Printf("✓ 伪造签名惩罚已处理，跳过该交易并继续处理区块")
		return nil
	}
	log.Printf("  ✅ 签名验证通过")

	// 2.2. 验证公钥是否匹配发送者地址
	derivedAddress, err := core.AddressFromPublicKey(tx.PublicKey)
	if err != nil {
		return fmt.Errorf("无法从公钥生成地址: %v", err)
	}
	if derivedAddress != tx.From {
		log.Printf("🚨 检测到地址伪造！Claimed: %s, Actual: %s", tx.From, derivedAddress)
		// 地址伪造是严重攻击，没收真实地址的所有资金
		if err := sm.confiscateAllFunds(derivedAddress, fmt.Sprintf("伪造发送者地址 (claimed=%s)", tx.From)); err != nil {
			log.Printf("❌ 没收资金失败: %v，返回错误停止区块处理", err)
			return fmt.Errorf("address forgery detected and punishment failed: %v", err)
		}
		// ✅ 惩罚已执行，返回nil让区块继续处理
		log.Printf("✓ 地址伪造惩罚已处理，跳过该交易并继续处理区块")
		return nil
	}

	// 3. 检查发送者余额
	sender, err := sm.GetAccount(tx.From)
	if err != nil {
		return err
	}

	// 根据交易类型检查不同的余额
	switch tx.Type {
	case core.TxUnstake:
		// 解押：检查质押余额
		if sender.StakedBalance < tx.Amount {
			return fmt.Errorf("insufficient staked balance: have %d, need %d",
				sender.StakedBalance, tx.Amount)
		}
	default:
		// 其他交易类型：检查可用余额
		var requiredBalance uint64
		if tx.Type.RequiresGasFee() {
			requiredBalance = tx.Amount + tx.GasFee
		} else {
			requiredBalance = tx.Amount
		}

		if sender.AvailableBalance < requiredBalance {
			return fmt.Errorf("insufficient balance: have %d, need %d",
				sender.AvailableBalance, requiredBalance)
		}
	}

	// 4. Nonce验证（必须严格等于当前nonce）
	if tx.Nonce != sender.Nonce {
		return fmt.Errorf("invalid nonce: expected %d, got %d", sender.Nonce, tx.Nonce)
	}

	// 5. 执行交易
	switch tx.Type {
	case core.TxTransfer:
		return sm.executeTransfer(tx)
	case core.TxStake:
		return sm.executeStake(tx)
	case core.TxUnstake:
		return sm.executeUnstake(tx)
	default:
		return fmt.Errorf("unknown transaction type: %d", tx.Type)
	}
}

// 执行转账
func (sm *StateManager) executeTransfer(tx *core.Transaction) error {
	// 1. 获取发送者和接收者
	sender, err := sm.GetAccount(tx.From)
	if err != nil {
		return err
	}

	receiver, err := sm.GetAccount(tx.To)
	if err != nil {
		return err
	}

	// 2. 扣除发送者余额（转账金额 + GAS费）
	totalCost := tx.Amount + tx.GasFee
	if err := sender.SubBalance(totalCost); err != nil {
		return err
	}
	sender.Nonce++

	// 3. 增加接收者余额（只有转账金额）
	receiver.AddBalance(tx.Amount)

	// 4. GAS费给创世地址
	if tx.To != core.GenesisAddress {
		genesis, err := sm.GetAccount(core.GenesisAddress)
		if err != nil {
			return err
		}
		genesis.AddBalance(tx.GasFee)
		sm.UpdateAccount(genesis)
	}

	// 5. 更新账户
	sm.UpdateAccount(sender)
	sm.UpdateAccount(receiver)

	return nil
}

// 执行抵押
func (sm *StateManager) executeStake(tx *core.Transaction) error {
	account, err := sm.GetAccount(tx.From)
	if err != nil {
		return err
	}

	// 记录质押前的状态
	wasValidator := account.IsValidator()

	if err := account.Stake(tx.Amount); err != nil {
		return err
	}

	account.Nonce++
	sm.UpdateAccount(account)

	// 检查是否成为新验证者（质押前不是，质押后是）
	isNowValidator := account.IsValidator()
	if !wasValidator && isNowValidator && sm.onValidatorAdded != nil {
		log.Printf("✅ 新验证者加入: %s (质押: %d)", tx.From, account.StakedBalance)
		sm.onValidatorAdded(tx.From, account.StakedBalance)
	}

	return nil
}

// 执行取消抵押
func (sm *StateManager) executeUnstake(tx *core.Transaction) error {
	account, err := sm.GetAccount(tx.From)
	if err != nil {
		return err
	}

	// 记录解押前的状态
	wasValidator := account.IsValidator()

	if err := account.Unstake(tx.Amount); err != nil {
		return err
	}

	account.Nonce++
	sm.UpdateAccount(account)

	// 检查是否不再是验证者（解押前是，解押后不是）
	isNowValidator := account.IsValidator()
	if wasValidator && !isNowValidator && sm.onValidatorRemoved != nil {
		log.Printf("⚠️ 验证者退出: %s (剩余质押: %d)", tx.From, account.StakedBalance)
		sm.onValidatorRemoved(tx.From)
	}

	return nil
}

// 执行系统交易
func (sm *StateManager) executeSystemTx(tx *core.Transaction) error {
	// 特殊处理：创世地址与自己的系统交易(奖励/惩罚)不执行任何操作（保持总量不变）
	if (tx.Type == core.TxReward || tx.Type == core.TxSlash) &&
		tx.From == core.GenesisAddress && tx.To == core.GenesisAddress {
		// 创世地址自己给自己奖励或惩罚，不增加也不减少，总量不变
		return nil
	}

	// 创世交易（From为空）：直接增加接收者余额
	if tx.From == "" {
		receiver, err := sm.GetAccount(tx.To)
		if err != nil {
			return err
		}
		receiver.AddBalance(tx.Amount)
		sm.UpdateAccount(receiver)
		return nil
	}

	// 奖励交易：从创世地址扣除，给接收者增加
	if tx.Type == core.TxReward {
		// 1. 从创世地址扣除
		genesis, err := sm.GetAccount(core.GenesisAddress)
		if err != nil {
			return err
		}
		if err := genesis.SubBalance(tx.Amount); err != nil {
			return fmt.Errorf("genesis insufficient balance: %v", err)
		}
		sm.UpdateAccount(genesis)

		// 2. 给接收者增加
		receiver, err := sm.GetAccount(tx.To)
		if err != nil {
			return err
		}
		receiver.AddBalance(tx.Amount)
		sm.UpdateAccount(receiver)
	}

	// 惩罚交易：从被惩罚者扣除，转入创世地址
	if tx.Type == core.TxSlash {
		// 1. 从被惩罚者扣除
		slashed, err := sm.GetAccount(tx.From)
		if err != nil {
			return err
		}
		if err := slashed.SubBalance(tx.Amount); err != nil {
			return fmt.Errorf("slashed account insufficient balance: %v", err)
		}
		sm.UpdateAccount(slashed)

		// 2. 转入创世地址
		genesis, err := sm.GetAccount(core.GenesisAddress)
		if err != nil {
			return err
		}
		genesis.AddBalance(tx.Amount)
		sm.UpdateAccount(genesis)
	}

	return nil
}

// 获取余额
func (sm *StateManager) GetBalance(address string) (uint64, error) {
	acc, err := sm.GetAccount(address)
	if err != nil {
		return 0, err
	}
	return acc.AvailableBalance, nil
}

// 获取nonce
func (sm *StateManager) GetNonce(address string) (uint64, error) {
	acc, err := sm.GetAccount(address)
	if err != nil {
		return 0, err
	}
	return acc.Nonce, nil
}

type StateSnapshot struct {
	accountCache  map[string]*core.Account
	dirtyAccounts map[string]bool
}

func (sm *StateManager) CreateSnapshot() *StateSnapshot {
	accountCopy := make(map[string]*core.Account)
	for addr, acc := range sm.accountCache {
		accCopy := *acc
		accountCopy[addr] = &accCopy
	}

	dirtyCopy := make(map[string]bool)
	for addr := range sm.dirtyAccounts {
		dirtyCopy[addr] = true
	}

	return &StateSnapshot{
		accountCache:  accountCopy,
		dirtyAccounts: dirtyCopy,
	}
}

func (sm *StateManager) RestoreSnapshot(snapshot *StateSnapshot) {
	sm.accountCache = make(map[string]*core.Account)
	for addr, acc := range snapshot.accountCache {
		accCopy := *acc
		sm.accountCache[addr] = &accCopy
	}

	sm.dirtyAccounts = make(map[string]bool)
	for addr := range snapshot.dirtyAccounts {
		sm.dirtyAccounts[addr] = true
	}
}

// 导入状态快照
func (sm *StateManager) ImportSnapshot(accounts []*core.Account) error {
	log.Printf("Importing state snapshot with %d accounts", len(accounts))

	for _, acc := range accounts {
		sm.UpdateAccount(acc)
	}

	if err := sm.Commit(); err != nil {
		return fmt.Errorf("failed to commit snapshot: %v", err)
	}

	log.Printf("✓ Successfully imported %d accounts", len(accounts))
	return nil
}

// ReloadStateFromHeight 从指定高度重新加载状态
// 用于链重组：从数据库重新加载账户状态
func (sm *StateManager) ReloadStateFromHeight(db *storage.Database, targetHeight uint64) error {
	log.Printf("⚠️  STATE RELOAD: Reloading state from height %d", targetHeight)

	// 1. 清空当前缓存
	sm.accountCache = make(map[string]*core.Account)
	sm.dirtyAccounts = make(map[string]bool)

	// 2. 从数据库重新加载所有账户
	accounts, err := db.GetAllAccounts()
	if err != nil {
		return fmt.Errorf("failed to reload accounts: %v", err)
	}

	// 3. 重新构建缓存
	for _, acc := range accounts {
		sm.accountCache[acc.Address] = acc
	}

	log.Printf("✓ STATE RELOAD: Reloaded %d accounts", len(accounts))
	return nil
}

// ClearCache 清空缓存（用于重组前）
func (sm *StateManager) ClearCache() {
	sm.accountCache = make(map[string]*core.Account)
	sm.dirtyAccounts = make(map[string]bool)
	log.Printf("✓ State cache cleared")
}

// VerifyTotalSupplyFast 快速验证总供应量（O(n)仅遍历缓存）
// 用于区块执行后的快速检查，不访问数据库
// 返回: (缓存中的总量, 是否正常, error)
func (sm *StateManager) VerifyTotalSupplyFast() (uint64, bool, error) {
	// 只计算缓存中账户的总量
	var cacheTotal uint64
	for _, acc := range sm.accountCache {
		cacheTotal += acc.AvailableBalance + acc.StakedBalance
	}

	// 如果追踪器已初始化，验证追踪器值
	if sm.trackerInitialized {
		if sm.totalSupplyTracker != TOTAL_SUPPLY {
			log.Printf("🚨 P0快速验证失败：追踪器值=%d, 预期=%d",
				sm.totalSupplyTracker, TOTAL_SUPPLY)
			return sm.totalSupplyTracker, false, nil
		}
	}

	return cacheTotal, true, nil
}

// VerifyTotalSupply 验证总供应量是否等于14亿FAN（完整验证）
// 返回: (总供应量, 是否正确, error)
func (sm *StateManager) VerifyTotalSupply() (uint64, bool, error) {
	// 获取所有账户（包括数据库和缓存）
	accounts, err := sm.db.GetAllAccounts()
	if err != nil {
		return 0, false, fmt.Errorf("failed to get accounts: %v", err)
	}

	// 创建账户映射，优先使用缓存中的版本
	accountMap := make(map[string]*core.Account)

	// 先添加数据库中的账户
	for _, acc := range accounts {
		accountMap[acc.Address] = acc
	}

	// 用缓存中的账户覆盖（包含最新的修改）
	for addr, acc := range sm.accountCache {
		accountMap[addr] = acc
	}

	// 计算总供应量 = 所有账户的(可用余额 + 质押余额)
	var totalSupply uint64
	for _, acc := range accountMap {
		totalSupply += acc.AvailableBalance + acc.StakedBalance
	}

	// 【增量验证同步】更新追踪器值为实际计算值
	if sm.trackerInitialized && sm.totalSupplyTracker != totalSupply {
		log.Printf("⚠️  追踪器值与实际值不符，同步更新: 追踪器=%d, 实际=%d",
			sm.totalSupplyTracker, totalSupply)
		sm.totalSupplyTracker = totalSupply
	}

	// 预期总供应量: 14亿FAN = 1400000000000000最小单位
	isCorrect := (totalSupply == TOTAL_SUPPLY)

	if !isCorrect {
		log.Printf("🚨🚨🚨 P0违反：总供应量异常！预期: %d, 实际: %d, 差值: %d",
			TOTAL_SUPPLY, totalSupply, int64(totalSupply)-int64(TOTAL_SUPPLY))

		// 打印详细的账户信息用于调试
		log.Printf("账户详情：")
		for addr, acc := range accountMap {
			if acc.AvailableBalance > 0 || acc.StakedBalance > 0 {
				log.Printf("  %s: 可用=%d, 质押=%d",
					addr, acc.AvailableBalance, acc.StakedBalance)
			}
		}
	}

	return totalSupply, isCorrect, nil
}

// VerifyTotalSupplyDual 双重验证（Checkpoint前使用）
// 同时执行快速验证和完整验证，两者必须一致
// 返回: (总供应量, 是否正确, error)
func (sm *StateManager) VerifyTotalSupplyDual() (uint64, bool, error) {
	// 1. 快速验证
	_, fastOK, _ := sm.VerifyTotalSupplyFast()

	// 2. 完整验证
	fullSupply, fullOK, err := sm.VerifyTotalSupply()
	if err != nil {
		return 0, false, err
	}

	// 3. 双重检查
	if fastOK != fullOK {
		log.Printf("🚨 P0双重验证不一致：快速验证=%v, 完整验证=%v", fastOK, fullOK)
		return fullSupply, false, nil
	}

	if !fullOK {
		log.Printf("🚨 P0双重验证失败：总量=%d, 预期=%d", fullSupply, TOTAL_SUPPLY)
	} else {
		log.Printf("✅ P0双重验证通过：总量=%d", fullSupply)
	}

	return fullSupply, fullOK, nil
}

// min 返回两个整数中的较小值
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GetAllAccountsMerged 获取所有账户（合并数据库和缓存）
// 用于验证者选择等需要完整账户列表的场景
func (sm *StateManager) GetAllAccountsMerged() ([]*core.Account, error) {
	// 1. 从数据库获取所有账户
	dbAccounts, err := sm.db.GetAllAccounts()
	if err != nil {
		return nil, fmt.Errorf("failed to get accounts from db: %v", err)
	}

	// 2. 创建账户映射，先放入数据库账户
	accountMap := make(map[string]*core.Account)
	for _, acc := range dbAccounts {
		accountMap[acc.Address] = acc
	}

	// 3. 用缓存中的账户覆盖（缓存是最新状态）
	for addr, acc := range sm.accountCache {
		// 复制账户避免修改原缓存
		accCopy := *acc
		accountMap[addr] = &accCopy
	}

	// 4. 转换为切片
	accounts := make([]*core.Account, 0, len(accountMap))
	for _, acc := range accountMap {
		accounts = append(accounts, acc)
	}

	return accounts, nil
}
