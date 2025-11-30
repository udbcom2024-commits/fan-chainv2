package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	"golang.org/x/crypto/sha3"
)

// 临时加密方案：使用ML-DSA签名的ECDH + AES-256-GCM
// 注意：这是过渡方案，等待ML-KEM-768库可用后替换

// EncryptedSession 加密会话
type EncryptedSession struct {
	aead   cipher.AEAD
	nonce  []byte
	cipher cipher.Block
}

// DeriveSessionKey 从共享密钥派生会话密钥
// 使用SHA3-256(sharedSecret) 生成256位密钥
func DeriveSessionKey(sharedSecret []byte) ([]byte, error) {
	if len(sharedSecret) == 0 {
		return nil, fmt.Errorf("empty shared secret")
	}

	// 使用SHA3-256派生密钥
	hash := sha3.Sum256(sharedSecret)
	return hash[:], nil
}

// NewEncryptedSession 创建加密会话
func NewEncryptedSession(sessionKey []byte) (*EncryptedSession, error) {
	if len(sessionKey) != 32 {
		return nil, fmt.Errorf("session key must be 32 bytes, got %d", len(sessionKey))
	}

	block, err := aes.NewCipher(sessionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %v", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %v", err)
	}

	return &EncryptedSession{
		aead:   aead,
		cipher: block,
	}, nil
}

// Encrypt 加密数据
func (s *EncryptedSession) Encrypt(plaintext []byte) ([]byte, error) {
	// 生成随机nonce
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %v", err)
	}

	// 加密：nonce + ciphertext
	ciphertext := s.aead.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt 解密数据
func (s *EncryptedSession) Decrypt(ciphertext []byte) ([]byte, error) {
	nonceSize := s.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	// 提取nonce和密文
	nonce := ciphertext[:nonceSize]
	encrypted := ciphertext[nonceSize:]

	// 解密
	plaintext, err := s.aead.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %v", err)
	}

	return plaintext, nil
}

// KeyExchangeRequest P2P密钥交换请求
type KeyExchangeRequest struct {
	PublicKey []byte // ML-DSA-65公钥
	Nonce     []byte // 32字节随机数
	Signature []byte // 对nonce的签名
}

// KeyExchangeResponse P2P密钥交换响应
type KeyExchangeResponse struct {
	PublicKey []byte // ML-DSA-65公钥
	Nonce     []byte // 32字节随机数
	Signature []byte // 对(请求nonce + 响应nonce)的签名
}

// GenerateKeyExchangeRequest 生成密钥交换请求
func GenerateKeyExchangeRequest(privateKey, publicKey []byte) (*KeyExchangeRequest, error) {
	// 生成随机nonce
	nonce := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %v", err)
	}

	// 签名nonce
	signature, err := Sign(privateKey, nonce)
	if err != nil {
		return nil, fmt.Errorf("failed to sign nonce: %v", err)
	}

	return &KeyExchangeRequest{
		PublicKey: publicKey,
		Nonce:     nonce,
		Signature: signature,
	}, nil
}

// VerifyKeyExchangeRequest 验证密钥交换请求
func VerifyKeyExchangeRequest(req *KeyExchangeRequest) bool {
	return Verify(req.PublicKey, req.Nonce, req.Signature)
}

// GenerateKeyExchangeResponse 生成密钥交换响应
func GenerateKeyExchangeResponse(privateKey, publicKey []byte, reqNonce []byte) (*KeyExchangeResponse, error) {
	// 生成随机nonce
	nonce := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %v", err)
	}

	// 签名(请求nonce + 响应nonce)
	combined := append(reqNonce, nonce...)
	signature, err := Sign(privateKey, combined)
	if err != nil {
		return nil, fmt.Errorf("failed to sign: %v", err)
	}

	return &KeyExchangeResponse{
		PublicKey: publicKey,
		Nonce:     nonce,
		Signature: signature,
	}, nil
}

// VerifyKeyExchangeResponse 验证密钥交换响应
func VerifyKeyExchangeResponse(resp *KeyExchangeResponse, reqNonce []byte) bool {
	combined := append(reqNonce, resp.Nonce...)
	return Verify(resp.PublicKey, combined, resp.Signature)
}

// DeriveSharedSecret 派生共享密钥
// 使用双方的nonce和公钥派生
func DeriveSharedSecret(localNonce, remoteNonce, localPublicKey, remotePublicKey []byte) []byte {
	// 组合所有输入：本地nonce + 远程nonce + 本地公钥 + 远程公钥
	combined := make([]byte, 0, len(localNonce)+len(remoteNonce)+len(localPublicKey)+len(remotePublicKey))
	combined = append(combined, localNonce...)
	combined = append(combined, remoteNonce...)
	combined = append(combined, localPublicKey...)
	combined = append(combined, remotePublicKey...)

	// SHA3-256哈希生成共享密钥
	hash := sha3.Sum256(combined)
	return hash[:]
}
