package crypto

import (
	"crypto/rand"
	"fmt"

	"filippo.io/mlkem768"
)

// ML-KEM-768 密钥封装机制（NIST FIPS 203标准）

// KEMKeyPair KEM密钥对
type KEMKeyPair struct {
	PublicKey  []byte
	PrivateKey []byte
}

// GenerateKEMKeyPair 生成ML-KEM-768密钥对
func GenerateKEMKeyPair() (*KEMKeyPair, error) {
	// 生成解封装密钥（包含私钥）
	dk, err := mlkem768.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate KEM key pair: %v", err)
	}

	// 提取封装密钥（公钥）
	ekBytes := dk.EncapsulationKey()

	// 序列化解封装密钥（私钥）
	// 注意：DecapsulationKey需要单独序列化，这里我们直接保存指针
	// 实际使用时需要保存完整的dk对象
	return &KEMKeyPair{
		PublicKey:  ekBytes,
		PrivateKey: dk.Bytes(), // DecapsulationKey的字节表示
	}, nil
}

// KEMEncapsulate 封装：生成共享密钥和密文
// 返回：sharedSecret (32字节), ciphertext, error
func KEMEncapsulate(publicKeyBytes []byte) ([]byte, []byte, error) {
	// 封装：生成共享密钥和密文
	// mlkem768.Encapsulate接受公钥字节，返回(ciphertext, sharedKey, error)
	ciphertext, sharedSecret, err := mlkem768.Encapsulate(publicKeyBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("encapsulation failed: %v", err)
	}

	return sharedSecret, ciphertext, nil
}

// KEMDecapsulate 解封装：从密文恢复共享密钥
// 返回：sharedSecret (32字节), error
func KEMDecapsulate(privateKeyBytes, ciphertext []byte) ([]byte, error) {
	// 从64字节种子反序列化DecapsulationKey
	dk, err := mlkem768.NewKeyFromSeed(privateKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %v", err)
	}

	// 解封装：恢复共享密钥
	sharedSecret, err := mlkem768.Decapsulate(dk, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decapsulation failed: %v", err)
	}

	return sharedSecret, nil
}

// KEMEncapsulateRand 封装（可指定随机数，用于测试）
func KEMEncapsulateRand(publicKeyBytes, randomness []byte) ([]byte, []byte, error) {
	if len(randomness) != 32 {
		return nil, nil, fmt.Errorf("randomness must be 32 bytes")
	}

	// 使用指定随机数封装
	// EncapsulateDerand返回(ciphertext, sharedKey, error)
	var randArray [32]byte
	copy(randArray[:], randomness)
	ciphertext, sharedSecret, err := mlkem768.EncapsulateDerand(publicKeyBytes, randArray[:])
	if err != nil {
		return nil, nil, fmt.Errorf("encapsulation failed: %v", err)
	}

	return sharedSecret, ciphertext, nil
}

// ValidateKEMPublicKey 验证公钥格式
func ValidateKEMPublicKey(publicKeyBytes []byte) bool {
	// 公钥长度应该是EncapsulationKeySize
	// 尝试封装来验证公钥有效性
	_, _, err := mlkem768.Encapsulate(publicKeyBytes)
	return err == nil
}

// ValidateKEMPrivateKey 验证私钥格式
func ValidateKEMPrivateKey(privateKeyBytes []byte) bool {
	// 私钥是64字节种子
	_, err := mlkem768.NewKeyFromSeed(privateKeyBytes)
	return err == nil
}

// GenerateRandomSeed 生成32字节随机种子（用于KEM操作）
func GenerateRandomSeed() ([]byte, error) {
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("failed to generate random seed: %v", err)
	}
	return seed, nil
}
