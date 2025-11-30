package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	"golang.org/x/crypto/sha3"
)

// ML-KEM-768 加密通信方案
// 符合NIST FIPS 203标准

// MLKEMKeyExchangeRequest ML-KEM密钥交换请求
type MLKEMKeyExchangeRequest struct {
	// ML-DSA-65身份公钥（用于身份认证）
	SignaturePublicKey []byte
	// ML-KEM-768公钥（用于密钥封装）
	KEMPublicKey []byte
	// 签名（对KEM公钥的签名）
	Signature []byte
}

// MLKEMKeyExchangeResponse ML-KEM密钥交换响应
type MLKEMKeyExchangeResponse struct {
	// ML-DSA-65身份公钥
	SignaturePublicKey []byte
	// ML-KEM-768封装后的密文
	Ciphertext []byte
	// 签名（对密文的签名）
	Signature []byte
}

// GenerateMLKEMKeyExchangeRequest 生成ML-KEM密钥交换请求
// dsaPrivKey: ML-DSA-65私钥（用于签名）
// dsaPubKey: ML-DSA-65公钥（身份标识）
func GenerateMLKEMKeyExchangeRequest(dsaPrivKey, dsaPubKey []byte) (*MLKEMKeyExchangeRequest, []byte, error) {
	// 1. 生成临时ML-KEM-768密钥对
	kemPair, err := GenerateKEMKeyPair()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate KEM key pair: %v", err)
	}

	// 2. 签名KEM公钥（证明拥有ML-DSA私钥）
	signature, err := Sign(dsaPrivKey, kemPair.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to sign KEM public key: %v", err)
	}

	req := &MLKEMKeyExchangeRequest{
		SignaturePublicKey: dsaPubKey,
		KEMPublicKey:       kemPair.PublicKey,
		Signature:          signature,
	}

	// 返回请求和KEM私钥（需要保存以便后续解封装）
	return req, kemPair.PrivateKey, nil
}

// VerifyMLKEMKeyExchangeRequest 验证ML-KEM密钥交换请求
func VerifyMLKEMKeyExchangeRequest(req *MLKEMKeyExchangeRequest) bool {
	// 验证签名：确认对方拥有ML-DSA私钥
	return Verify(req.SignaturePublicKey, req.KEMPublicKey, req.Signature)
}

// GenerateMLKEMKeyExchangeResponse 生成ML-KEM密钥交换响应
// dsaPrivKey: 本地ML-DSA-65私钥
// dsaPubKey: 本地ML-DSA-65公钥
// reqKEMPubKey: 请求方的ML-KEM-768公钥
func GenerateMLKEMKeyExchangeResponse(dsaPrivKey, dsaPubKey, reqKEMPubKey []byte) (*MLKEMKeyExchangeResponse, []byte, error) {
	// 1. 使用请求方的KEM公钥封装，生成共享密钥
	sharedSecret, ciphertext, err := KEMEncapsulate(reqKEMPubKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encapsulate: %v", err)
	}

	// 2. 签名密文（证明拥有ML-DSA私钥）
	signature, err := Sign(dsaPrivKey, ciphertext)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to sign ciphertext: %v", err)
	}

	resp := &MLKEMKeyExchangeResponse{
		SignaturePublicKey: dsaPubKey,
		Ciphertext:         ciphertext,
		Signature:          signature,
	}

	// 返回响应和共享密钥
	return resp, sharedSecret, nil
}

// VerifyMLKEMKeyExchangeResponse 验证ML-KEM密钥交换响应
func VerifyMLKEMKeyExchangeResponse(resp *MLKEMKeyExchangeResponse) bool {
	// 验证签名：确认对方拥有ML-DSA私钥
	return Verify(resp.SignaturePublicKey, resp.Ciphertext, resp.Signature)
}

// DecapsulateSharedSecret 从响应中解封装共享密钥
// kemPrivKey: 本地KEM私钥
// ciphertext: 对方发送的密文
func DecapsulateSharedSecret(kemPrivKey, ciphertext []byte) ([]byte, error) {
	sharedSecret, err := KEMDecapsulate(kemPrivKey, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("failed to decapsulate: %v", err)
	}
	return sharedSecret, nil
}

// DeriveSessionKeyFromKEM 从ML-KEM共享密钥派生会话密钥
// 使用SHA3-256确保密钥纯度
func DeriveSessionKeyFromKEM(sharedSecret []byte) ([]byte, error) {
	if len(sharedSecret) != 32 {
		return nil, fmt.Errorf("shared secret must be 32 bytes, got %d", len(sharedSecret))
	}

	// ML-KEM-768的共享密钥已经是32字节，直接使用
	// 但我们仍然通过SHA3-256处理一次，增加安全边际
	hash := sha3.Sum256(sharedSecret)
	return hash[:], nil
}

// MLKEMEncryptedSession ML-KEM加密会话
type MLKEMEncryptedSession struct {
	aead cipher.AEAD
}

// NewMLKEMEncryptedSession 创建ML-KEM加密会话
func NewMLKEMEncryptedSession(sharedSecret []byte) (*MLKEMEncryptedSession, error) {
	// 派生会话密钥
	sessionKey, err := DeriveSessionKeyFromKEM(sharedSecret)
	if err != nil {
		return nil, err
	}

	// 创建AES-256-GCM加密器
	block, err := aes.NewCipher(sessionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %v", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %v", err)
	}

	return &MLKEMEncryptedSession{
		aead: aead,
	}, nil
}

// Encrypt 加密数据
func (s *MLKEMEncryptedSession) Encrypt(plaintext []byte) ([]byte, error) {
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
func (s *MLKEMEncryptedSession) Decrypt(ciphertext []byte) ([]byte, error) {
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
