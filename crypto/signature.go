package crypto

import (
	"crypto/rand"
	"crypto"
	"fmt"
	"io"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
	"golang.org/x/crypto/sha3"
)

// ML-DSA-65 签名和验证

// 生成密钥对
func GenerateKeyPair() (publicKey, privateKey []byte, err error) {
	pub, priv, err := mldsa65.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	pubBytes, err := pub.MarshalBinary()
	if err != nil {
		return nil, nil, err
	}

	privBytes, err := priv.MarshalBinary()
	if err != nil {
		return nil, nil, err
	}

	return pubBytes, privBytes, nil
}

// 签名
func Sign(privateKeyBytes, message []byte) ([]byte, error) {
	if len(privateKeyBytes) == 0 {
		return nil, fmt.Errorf("empty private key")
	}

	var priv mldsa65.PrivateKey
	if err := priv.UnmarshalBinary(privateKeyBytes); err != nil {
		return nil, fmt.Errorf("invalid private key (len=%d): %v", len(privateKeyBytes), err)
	}

	// ML-DSA-65使用确定性签名
	signature, err := priv.Sign(rand.Reader, message, crypto.Hash(0))
	if err != nil {
		return nil, fmt.Errorf("signing failed: %v", err)
	}

	return signature, nil
}

// 验证签名
func Verify(publicKeyBytes, message, signature []byte) bool {
	var pub mldsa65.PublicKey
	if err := pub.UnmarshalBinary(publicKeyBytes); err != nil {
		return false
	}

	return mldsa65.Verify(&pub, message, nil, signature)
}

// 从私钥导出公钥
func PrivateKeyToPublic(privateKeyBytes []byte) ([]byte, error) {
	var priv mldsa65.PrivateKey
	if err := priv.UnmarshalBinary(privateKeyBytes); err != nil {
		return nil, err
	}

	// ML-DSA-65 私钥包含公钥，需要重新生成或存储
	// 这里简化处理：要求调用者同时存储公钥
	return nil, fmt.Errorf("please store public key separately")
}

// VRF相关（简化版：使用签名模拟VRF）
// 注意：这不是真正的VRF，只是用于MVP阶段
// 后续需要使用真正的VRF库（如 github.com/coniks-sys/coniks-go/crypto/vrf）

// VRF证明和输出
type VRFProof struct {
	Proof  []byte
	Output []byte
}

// 计算VRF（MVP：用签名代替）
func ComputeVRF(privateKeyBytes, seed []byte) (*VRFProof, error) {
	// 签名种子
	signature, err := Sign(privateKeyBytes, seed)
	if err != nil {
		return nil, err
	}

	// 输出 = Hash(signature)
	output := hash256(signature)

	return &VRFProof{
		Proof:  signature,
		Output: output,
	}, nil
}

// 验证VRF（MVP：验证签名）
func VerifyVRF(publicKeyBytes, seed []byte, proof *VRFProof) bool {
	// 验证签名
	var pub mldsa65.PublicKey
	if err := pub.UnmarshalBinary(publicKeyBytes); err != nil {
		return false
	}

	if !mldsa65.Verify(&pub, seed, nil, proof.Proof) {
		return false
	}

	// 验证输出
	expectedOutput := hash256(proof.Proof)
	if len(expectedOutput) != len(proof.Output) {
		return false
	}

	for i := range expectedOutput {
		if expectedOutput[i] != proof.Output[i] {
			return false
		}
	}

	return true
}

// 辅助函数：SHA3-256哈希
func hash256(data []byte) []byte {
	hash := sha3.Sum256(data)
	return hash[:]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// 生成随机数
func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, err
	}
	return b, nil
}
