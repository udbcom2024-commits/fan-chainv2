package core

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"time"

	"golang.org/x/crypto/sha3"
)

// ========== 硬编码常量（永不改变） ==========
const (
	// 总供应量 - 硬编码，保证永不改变
	TotalSupply = 1400000000000000 // 14亿FAN（最小单位）

	// 创世地址 - 硬编码，保证永不改变
	GenesisAddress = "F25gxrj3tppc07hunne7hztvde5gkaw78f3xa"

	// 验证奖励（暂时保留）
	ValidateReward = 1000000 // 1 FAN
)

// ========== 从consensus.json加载的共识参数 ==========
var (
	consensusConfig = GetConsensusConfig()
)

// 代币精度
func FANDecimals() int {
	return consensusConfig.ChainParams.FANDecimals
}

func FANUnit() uint64 {
	return consensusConfig.ChainParams.FANUnit
}

// GAS费用
func MinGasFee() uint64 {
	return consensusConfig.EconomicParams.MinGasFee
}

func MaxGasFee() uint64 {
	return consensusConfig.EconomicParams.MaxGasFee
}

// 时间戳验证
func MaxTimestampDrift() int64 {
	return consensusConfig.BlockParams.MaxTimestampDrift
}

// 奖励（动态计算）
func BlockReward() uint64 {
	return consensusConfig.EconomicParams.BaseBlockReward
}

// 出块参数
func BlockInterval() int {
	return consensusConfig.BlockParams.BlockIntervalSeconds
}

func FinalityBlocks() int {
	return consensusConfig.BlockParams.FinalityBlocks
}

func FinalityTime() int {
	return consensusConfig.BlockParams.BlockIntervalSeconds * consensusConfig.BlockParams.FinalityBlocks
}

// 验证者
func ValidatorStakeRequired() uint64 {
	return consensusConfig.EconomicParams.ValidatorStakeRequired
}

func MaxValidators() int {
	return consensusConfig.ValidatorParams.MaxValidators
}

func ActiveValidatorSet() int {
	return consensusConfig.ValidatorParams.ActiveValidatorSet
}

// 创世区块时间戳
func GenesisTimestamp() int64 {
	return consensusConfig.ChainParams.GenesisTimestamp
}

// Hash类型
type Hash [32]byte

func (h Hash) Bytes() []byte {
	return h[:]
}

func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}
func BytesToHash(b []byte) Hash {
	var h Hash
	copy(h[:], b)
	return h
}

// 计算哈希
func CalculateHash(data []byte) Hash {
	return sha3.Sum256(data)
}

// Uint64转字节
func Uint64ToBytes(n uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	return b
}

// 字节转Uint64
func BytesToUint64(b []byte) uint64 {
	return binary.BigEndian.Uint64(b)
}

// 时间戳
func CurrentTimestamp() int64 {
	return time.Now().Unix()
}

// JSON序列化辅助函数
func ToJSON(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func FromJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// 地址常量
const (
	AddressPrefix = "F"
	AddressLength = 37 // 包含前缀
	ChecksumBytes = 3
	AddressBytes  = 20
)

// Base36字符集
const base36Alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// 从公钥派生地址
func DeriveAddress(publicKey []byte) string {
	// 1. 对公钥进行SHA3-256哈希
	hash := sha3.Sum256(publicKey)
	addressData := hash[:AddressBytes]

	// 2. 计算校验和
	checksumHash := sha3.Sum256(addressData)
	checksum := checksumHash[:ChecksumBytes]

	// 3. 合并地址数据和校验和
	fullData := make([]byte, AddressBytes+ChecksumBytes)
	copy(fullData[:AddressBytes], addressData)
	copy(fullData[AddressBytes:], checksum)

	// 4. Base36编码
	base36String := base36Encode(fullData)

	// 5. 填充到固定长度
	for len(base36String) < AddressLength-1 {
		base36String = "0" + base36String
	}

	// 6. 添加前缀
	return AddressPrefix + base36String
}

// Base36编码
func base36Encode(data []byte) string {
	num := new(big.Int).SetBytes(data)
	if num.Sign() == 0 {
		return "0"
	}

	base := big.NewInt(36)
	result := ""
	for num.Sign() > 0 {
		mod := new(big.Int)
		num.DivMod(num, base, mod)
		result = string(base36Alphabet[mod.Int64()]) + result
	}
	return result
}

// 验证地址格式
func ValidateAddress(addr string) bool {
	// 1. 检查长度
	if len(addr) != AddressLength {
		return false
	}

	// 2. 检查前缀
	if addr[0] != AddressPrefix[0] {
		return false
	}

	// 3. 检查字符集
	for _, c := range addr[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z')) {
			return false
		}
	}

	// 4. 解码并验证校验和
	data := base36Decode(addr[1:])
	if len(data) != AddressBytes+ChecksumBytes {
		return false
	}

	addressData := data[:AddressBytes]
	checksum := data[AddressBytes:]
	expectedChecksumHash := sha3.Sum256(addressData)
	expectedChecksum := expectedChecksumHash[:ChecksumBytes]

	for i := 0; i < ChecksumBytes; i++ {
		if checksum[i] != expectedChecksum[i] {
			return false
		}
	}

	return true
}

// Base36解码
func base36Decode(s string) []byte {
	num := new(big.Int)
	base := big.NewInt(36)

	for _, c := range s {
		num.Mul(num, base)
		var digit int64
		if c >= '0' && c <= '9' {
			digit = int64(c - '0')
		} else if c >= 'a' && c <= 'z' {
			digit = int64(c-'a') + 10
		} else {
			return nil
		}
		num.Add(num, big.NewInt(digit))
	}

	bytes := num.Bytes()
	if len(bytes) < AddressBytes+ChecksumBytes {
		padded := make([]byte, AddressBytes+ChecksumBytes)
		copy(padded[AddressBytes+ChecksumBytes-len(bytes):], bytes)
		return padded
	}
	return bytes
}

// 从公钥生成地址（用于验证签名）
func AddressFromPublicKey(publicKeyBytes []byte) (string, error) {
	hash := sha3.Sum256(publicKeyBytes)
	addressData := hash[:AddressBytes]
	checksumHash := sha3.Sum256(addressData)
	checksum := checksumHash[:ChecksumBytes]

	fullData := make([]byte, AddressBytes+ChecksumBytes)
	copy(fullData[:AddressBytes], addressData)
	copy(fullData[AddressBytes:], checksum)

	base36String := base36Encode(fullData)
	for len(base36String) < AddressLength-1 {
		base36String = "0" + base36String
	}

	return AddressPrefix + base36String, nil
}
