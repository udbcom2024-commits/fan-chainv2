package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// 主密钥（硬编码，未来版本可能改为配置文件或环境变量）
// 注意：这是示例密钥，实际部署时应该使用真正的随机密钥
var masterKey = []byte("FAN-Chain-Master-Key-Change-This-In-Production-32bytes!!")

// DeriveKeyFromHeight 基于区块高度派生密钥
// 每1000个区块轮换一次密钥
func DeriveKeyFromHeight(height uint64) ([]byte, error) {
	// 计算密钥epoch（每1000区块一个epoch）
	epoch := height / 1000

	// 使用HKDF派生密钥
	info := fmt.Sprintf("FAN-Chain-Data-Encryption-Epoch-%d", epoch)
	hkdfReader := hkdf.New(sha256.New, masterKey, nil, []byte(info))

	// 派生32字节密钥（AES-256）
	derivedKey := make([]byte, 32)
	if _, err := io.ReadFull(hkdfReader, derivedKey); err != nil {
		return nil, fmt.Errorf("failed to derive key: %v", err)
	}

	return derivedKey, nil
}

// EncryptData 使用AES-256-GCM加密数据
func EncryptData(plaintext []byte, height uint64) ([]byte, error) {
	// 派生密钥
	key, err := DeriveKeyFromHeight(height)
	if err != nil {
		return nil, err
	}

	// 创建AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %v", err)
	}

	// 创建GCM模式
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %v", err)
	}

	// 生成随机nonce
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %v", err)
	}

	// 加密数据（nonce + ciphertext + tag）
	ciphertext := aesGCM.Seal(nonce, nonce, plaintext, nil)

	return ciphertext, nil
}

// DecryptData 使用AES-256-GCM解密数据
func DecryptData(ciphertext []byte, height uint64) ([]byte, error) {
	// 派生密钥
	key, err := DeriveKeyFromHeight(height)
	if err != nil {
		return nil, err
	}

	// 创建AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %v", err)
	}

	// 创建GCM模式
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %v", err)
	}

	// 检查密文长度
	nonceSize := aesGCM.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	// 提取nonce和实际密文
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

	// 解密数据
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %v", err)
	}

	return plaintext, nil
}
