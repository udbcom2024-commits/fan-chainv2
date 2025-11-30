package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
	"golang.org/x/crypto/sha3"
)

// 交易类型
type TxType uint8

const (
	TxTransfer TxType = 0
	TxStake    TxType = 1
	TxUnstake  TxType = 2
)

// 最小GAS费用
const MinGasFee uint64 = 1

// Transaction结构体
type Transaction struct {
	Type      TxType `json:"type"`
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    uint64 `json:"amount"`
	GasFee    uint64 `json:"gas_fee"`
	Nonce     uint64 `json:"nonce"`
	Timestamp int64  `json:"timestamp"`
	Data      []byte `json:"data,omitempty"`
	GasLimit  uint64 `json:"gas_limit,omitempty"`
	Signature []byte `json:"signature"`
	PublicKey []byte `json:"public_key"`
}

func main() {
	// 命令行参数
	fromAddr := flag.String("from", "", "解押地址 (必填)")
	amount := flag.Uint64("amount", 0, "解押金额（单位：FAN）")
	gasFee := flag.Uint64("gas", 0, "GAS费用 (解押交易不收取gas费,默认0)")
	privKeyFile := flag.String("key", "", "私钥文件路径 (必填)")
	pubKeyFile := flag.String("pub", "", "公钥文件路径 (必填)")
	nodeURL := flag.String("node", "http://localhost:9000", "节点API地址")

	flag.Parse()

	// 验证必填参数
	if *fromAddr == "" || *amount == 0 {
		fmt.Println("错误：缺少必填参数")
		fmt.Println()
		fmt.Println("使用示例：")
		fmt.Println("  unstake.exe -from F7biz3m3dfq966u5vqj2wcts4z21nmdbalsyy -amount 10 -key ../../addr/mywallet/wallet_private.key -pub ../../addr/mywallet/wallet_public.key")
		fmt.Println()
		fmt.Println("Windows直接用: unstake.exe ...，Git Bash用: ./unstake.exe ...")
		fmt.Println()
		fmt.Println("参数说明：")
		fmt.Println("  -from:   解押地址（已质押的验证者）")
		fmt.Println("  -amount: 解押金额（单位：FAN，解押后将不再参与共识）")
		fmt.Println("  -key:    私钥文件路径")
		fmt.Println("  -pub:    公钥文件路径")
		fmt.Println("  -node:   节点API地址（默认 http://localhost:9000）")
		fmt.Println("  -gas:    手续费（解押交易不收取gas费,默认0）")
		fmt.Println()
		os.Exit(1)
	}

	if *privKeyFile == "" || *pubKeyFile == "" {
		log.Fatal("错误：需要指定私钥和公钥文件 (-key 和 -pub)")
	}

	// 读取私钥
	privKeyBytes, err := os.ReadFile(*privKeyFile)
	if err != nil {
		log.Fatalf("读取私钥失败: %v", err)
	}

	// 读取公钥
	pubKeyBytes, err := os.ReadFile(*pubKeyFile)
	if err != nil {
		log.Fatalf("读取公钥失败: %v", err)
	}

	fmt.Println("FAN链解押工具")
	fmt.Println("==============")
	fmt.Println()

	// 查询账户 nonce
	nonce, err := queryAccountNonce(*nodeURL, *fromAddr)
	if err != nil {
		log.Fatalf("查询 nonce 失败: %v\n", err)
	}

	// 创建解押交易（Type=2, To为空）
	tx := &Transaction{
		Type:      TxUnstake,
		From:      *fromAddr,
		To:        "",  // 解押交易To为空
		Amount:    *amount * 1000000,  // 转换为最小单位
		GasFee:    *gasFee,
		Nonce:     nonce,
		Timestamp: time.Now().Unix(),
		PublicKey: pubKeyBytes,
	}

	// 获取签名数据
	signData := getSignData(tx)

	// 签名交易
	signature, err := signTransaction(privKeyBytes, signData)
	if err != nil {
		log.Fatalf("签名失败: %v", err)
	}
	tx.Signature = signature

	// 计算交易哈希
	txHash := calculateTxHash(signData)

	// 显示交易信息
	fmt.Printf("解押信息：\n")
	fmt.Printf("  类型：      解押（Unstake）\n")
	fmt.Printf("  地址：      %s\n", *fromAddr)
	fmt.Printf("  解押金额：  %d FAN\n", *amount)
	fmt.Printf("  手续费：    %d (最小单位)\n", *gasFee)
	fmt.Printf("  时间戳：    %d\n", tx.Timestamp)
	fmt.Printf("  交易哈希：  %s\n", txHash)
	fmt.Println()

	// 发送交易到节点
	fmt.Printf("正在发送解押交易到节点: %s\n", *nodeURL)
	if err := sendTransaction(tx, *nodeURL); err != nil {
		log.Fatalf("发送交易失败: %v", err)
	}

	fmt.Println()
	fmt.Println("✓ 解押成功！质押金额已返还到账户可用余额")
	fmt.Printf("交易哈希: %s\n", txHash)
	fmt.Println()
	fmt.Println("查询交易状态：")
	fmt.Printf("  curl %s/transaction/%s\n", *nodeURL, txHash)
}

// 获取签名数据
func getSignData(tx *Transaction) []byte {
	buf := new(bytes.Buffer)
	buf.WriteByte(byte(tx.Type))
	buf.WriteString(tx.From)
	buf.WriteString(tx.To)
	buf.Write(uint64ToBytes(tx.Amount))
	buf.Write(uint64ToBytes(tx.GasFee))
	buf.Write(uint64ToBytes(tx.Nonce))
	buf.Write(uint64ToBytes(uint64(tx.Timestamp)))
	if len(tx.Data) > 0 {
		buf.Write(tx.Data)
	}
	return buf.Bytes()
}

// Uint64转字节（大端序）
func uint64ToBytes(n uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	return b
}

// 签名交易（使用ML-DSA-65）
func signTransaction(privateKeyBytes, message []byte) ([]byte, error) {
	if len(privateKeyBytes) == 0 {
		return nil, fmt.Errorf("私钥为空")
	}

	var priv mldsa65.PrivateKey
	if err := priv.UnmarshalBinary(privateKeyBytes); err != nil {
		return nil, fmt.Errorf("私钥格式错误 (长度=%d): %v", len(privateKeyBytes), err)
	}

	signature, err := priv.Sign(rand.Reader, message, crypto.Hash(0))
	if err != nil {
		return nil, fmt.Errorf("签名失败: %v", err)
	}

	return signature, nil
}

// 计算交易哈希
func calculateTxHash(signData []byte) string {
	hash := sha3.Sum256(signData)
	return hex.EncodeToString(hash[:])
}

// 发送交易到节点
func sendTransaction(tx *Transaction, nodeURL string) error {
	txJSON, err := json.Marshal(tx)
	if err != nil {
		return fmt.Errorf("序列化交易失败: %v", err)
	}

	url := nodeURL + "/transaction"
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(txJSON))
	if err != nil {
		return fmt.Errorf("HTTP请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("节点返回错误 (状态码=%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("节点响应: %s\n", string(body))
		return nil
	}

	if msg, ok := result["message"].(string); ok {
		fmt.Printf("节点响应: %s\n", msg)
	}
	if hash, ok := result["hash"].(string); ok {
		fmt.Printf("交易哈希: %s\n", hash)
	}

	return nil
}

// 查询账户 nonce
func queryAccountNonce(nodeURL, address string) (uint64, error) {
	url := fmt.Sprintf("%s/balance/%s", nodeURL, address)
	resp, err := http.Get(url)
	if err != nil {
		return 0, fmt.Errorf("HTTP请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("节点返回错误 (状态码=%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Nonce uint64 `json:"nonce"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("解析响应失败: %v", err)
	}

	return result.Nonce, nil
}
