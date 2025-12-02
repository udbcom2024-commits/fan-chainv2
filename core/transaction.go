package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// 交易类型
type TxType uint8

const (
	TxTransfer TxType = 0 // 普通转账 - 收取gas fee
	TxStake    TxType = 1 // 抵押 - 不收gas fee
	TxUnstake  TxType = 2 // 取消抵押 - 不收gas fee
	TxReward   TxType = 3 // 系统奖励 - 不收gas fee
	TxSlash    TxType = 4 // 惩罚 - 不收gas fee
)

// RequiresGasFee 判断交易类型是否需要收取gas费
// 只有普通转账(TxTransfer)需要收取gas费
func (t TxType) RequiresGasFee() bool {
	return t == TxTransfer
}

// IsSystemTx 判断是否为系统交易(不需要用户签名)
func (t TxType) IsSystemTx() bool {
	return t == TxReward || t == TxSlash
}

// 交易结构
type Transaction struct {
	Type      TxType `json:"type"`
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    uint64 `json:"amount"`
	GasFee    uint64 `json:"gas_fee"`
	Nonce     uint64 `json:"nonce"`
	Timestamp int64  `json:"timestamp"`

	// 扩展字段（未来）
	Data     []byte `json:"data,omitempty"`
	GasLimit uint64 `json:"gas_limit,omitempty"`

	// 签名
	Signature []byte `json:"signature"`
	PublicKey []byte `json:"public_key"`
}

// 创建转账交易
func NewTransferTx(from, to string, amount, gasFee, nonce uint64) *Transaction {
	return &Transaction{
		Type:      TxTransfer,
		From:      from,
		To:        to,
		Amount:    amount,
		GasFee:    gasFee,
		Nonce:     nonce,
		Timestamp: CurrentTimestamp(),
	}
}

// 创建奖励交易（系统生成，无需签名）
func NewRewardTx(to string, amount uint64) *Transaction {
	return &Transaction{
		Type:      TxReward,
		From:      GenesisAddress,
		To:        to,
		Amount:    amount,
		GasFee:    0,
		Nonce:     0,
		Timestamp: CurrentTimestamp(),
	}
}

// 计算交易哈希
func (tx *Transaction) Hash() Hash {
	// 不包含signature的数据
	data := tx.SignData()
	return CalculateHash(data)
}

// 获取签名数据
// 注意：nonce不参与签名，由节点在SubmitTransaction中自动分配
// 这样可以防止客户端nonce被篡改，简化客户端逻辑
func (tx *Transaction) SignData() []byte {
	buf := new(bytes.Buffer)

	buf.WriteByte(byte(tx.Type))
	buf.WriteString(tx.From)
	buf.WriteString(tx.To)
	buf.Write(Uint64ToBytes(tx.Amount))
	buf.Write(Uint64ToBytes(tx.GasFee))
	// Nonce不参与签名 - 由节点自动分配
	buf.Write(Uint64ToBytes(uint64(tx.Timestamp)))

	if len(tx.Data) > 0 {
		buf.Write(tx.Data)
	}

	return buf.Bytes()
}

// 签名交易
func (tx *Transaction) Sign(privateKey []byte) error {
	// 导入crypto包需要在文件顶部添加
	// 这里先定义接口，实际签名在外部调用
	return fmt.Errorf("use crypto.Sign() to sign transaction")
}

// 验证交易签名
func (tx *Transaction) VerifySignature() error {
	// 1. 系统交易不需要签名
	if tx.Type.IsSystemTx() {
		return nil
	}

	// 2. 检查签名和公钥
	if len(tx.Signature) == 0 {
		return fmt.Errorf("missing signature")
	}

	if len(tx.PublicKey) == 0 {
		return fmt.Errorf("missing public key")
	}

	// 3. 验证签名（需要导入crypto包）
	// 实际验证在state manager中进行
	return nil
}

// 验证交易
// skipTimestampCheck: true表示跳过时间戳验证（用于同步历史区块），false表示严格验证（用于API提交的新交易）
func (tx *Transaction) Validate(skipTimestampCheck bool) error {
	// 1. 检查发送者地址
	if tx.From == "" {
		return fmt.Errorf("invalid from address")
	}

	// 2. 检查接收者地址
	if tx.To == "" {
		return fmt.Errorf("invalid to address")
	}

	// 3. 禁止非创世地址给自己转账（创世地址和质押/取消质押除外）
	if tx.From == tx.To && tx.From != GenesisAddress && tx.Type != TxStake && tx.Type != TxUnstake {
		return fmt.Errorf("cannot transfer to yourself (only genesis address allowed)")
	}

	// 4. 检查金额
	if tx.Amount == 0 && tx.Type == TxTransfer {
		return fmt.Errorf("amount must be positive")
	}

	// 5. 检查GAS费用(使用全局方法判断)
	if tx.Type.RequiresGasFee() {
		if tx.GasFee < MinGasFee() || tx.GasFee > MaxGasFee() {
			return fmt.Errorf("invalid gas fee: %d", tx.GasFee)
		}
	} else {
		// 不收gas fee的交易,gas fee应该为0
		if tx.GasFee != 0 {
			return fmt.Errorf("transaction type %s should not have gas fee", tx.TypeString())
		}
	}

	// 6. 检查Data字段长度（从共识参数读取）
	consensusConfig := GetConsensusConfig()
	if uint64(len(tx.Data)) > consensusConfig.TransactionParams.MaxDataSize {
		return fmt.Errorf("data field too large: %d bytes (max allowed: %d bytes)",
			len(tx.Data), consensusConfig.TransactionParams.MaxDataSize)
	}

	// 7. 验证时间戳（防止时间戳伪造）
	// 注意：同步历史区块时跳过此检查，因为历史交易的时间戳可能与当前时间相差很远
	// 时间戳使用毫秒级
	if !skipTimestampCheck {
		currentTime := time.Now().UnixMilli() // 毫秒级
		timeDiff := tx.Timestamp - currentTime
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}
		if timeDiff > MaxTimestampDrift() {
			return fmt.Errorf("invalid timestamp: transaction time %d differs from current time %d by %d ms (max allowed: %d ms)",
				tx.Timestamp, currentTime, timeDiff, MaxTimestampDrift())
		}
	}

	// 8. 系统交易不需要签名
	if tx.Type.IsSystemTx() {
		return nil
	}

	// 9. 用户交易需要签名
	if len(tx.Signature) == 0 {
		return fmt.Errorf("missing signature")
	}

	if len(tx.PublicKey) == 0 {
		return fmt.Errorf("missing public key")
	}

	return nil
}

// JSON序列化
func (tx *Transaction) ToJSON() ([]byte, error) {
	return json.Marshal(tx)
}

func (tx *Transaction) FromJSON(data []byte) error {
	return json.Unmarshal(data, tx)
}

// 字符串表示
func (tx *Transaction) String() string {
	return fmt.Sprintf("Tx{%s: %s->%s %d FAN, fee:%d, nonce:%d}",
		tx.TypeString(), tx.From, tx.To,
		tx.Amount/FANUnit(), tx.GasFee, tx.Nonce)
}

func (tx *Transaction) TypeString() string {
	switch tx.Type {
	case TxTransfer:
		return "Transfer"
	case TxStake:
		return "Stake"
	case TxUnstake:
		return "Unstake"
	case TxReward:
		return "Reward"
	case TxSlash:
		return "Slash"
	default:
		return "Unknown"
	}
}
