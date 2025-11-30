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
const MinGasFee uint64 = 1 // 0.000001 FAN

// Transaction结构体（与core.Transaction一致）
type Transaction struct {
	Type      TxType `json:"type"`
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    uint64 `json:"amount"`
	GasFee    uint64 `json:"gas_fee"`
	Nonce     uint64 `json:"nonce"`
	Timestamp int64  `json:"timestamp"`

	Data     []byte `json:"data,omitempty"`
	GasLimit uint64 `json:"gas_limit,omitempty"`

	Signature []byte `json:"signature"`
	PublicKey []byte `json:"public_key"`
}

func main() {
	// 命令行参数
	fromAddr := flag.String("from", "", "发送者地址 (必填)")
	toAddr := flag.String("to", "", "接收者地址 (必填)")
	amount := flag.Uint64("amount", 0, "转账金额/最小单位 (必填，1 FAN = 1000000)")
	gasFee := flag.Uint64("gas", MinGasFee, "GAS费用 (默认1，即0.000001 FAN)")
	privKeyFile := flag.String("key", "", "私钥文件路径 (必填)")
	pubKeyFile := flag.String("pub", "", "公钥文件路径 (必填)")
	nodeURL := flag.String("node", "http://localhost:9000", "节点API地址")
	output := flag.String("out", "", "仅生成交易JSON文件，不发送 (可选)")

	flag.Parse()

	// 验证必填参数
	if *fromAddr == "" || *toAddr == "" || *amount == 0 {
		fmt.Println("错误：缺少必填参数")
		fmt.Println()
		fmt.Println("使用示例：")
		fmt.Println("  go run transfer.go \\")
		fmt.Println("    -from F0rkjuwww2dtoocnd42h9e8uaoxpzgptgoe10 \\")
		fmt.Println("    -to F1abc... \\")
		fmt.Println("    -amount 100000000 \\")
		fmt.Println("    -key ./keys/mywallet/wallet_private.key \\")
		fmt.Println("    -pub ./keys/mywallet/wallet_public.key")
		fmt.Println()
		fmt.Println("参数说明：")
		fmt.Println("  -amount: 转账金额（最小单位，1 FAN = 1000000）")
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

	fmt.Println("FAN链转账工具")
	fmt.Println("==============")
	fmt.Println()

	// 创建交易（nonce由节点自动分配，签名不包含nonce）
	tx := &Transaction{
		Type:      TxTransfer,
		From:      *fromAddr,
		To:        *toAddr,
		Amount:    *amount,
		GasFee:    *gasFee,
		Nonce:     0, // 将由节点自动分配
		Timestamp: time.Now().Unix(),
		PublicKey: pubKeyBytes,
	}

	// 获取签名数据（与core.Transaction.SignData()一致）
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
	fmt.Printf("交易信息：\n")
	fmt.Printf("  类型：      转账\n")
	fmt.Printf("  发送方：    %s\n", *fromAddr)
	fmt.Printf("  接收方：    %s\n", *toAddr)
	fmt.Printf("  金额：      %.6f FAN\n", float64(*amount)/1000000.0)
	fmt.Printf("  手续费：    %.6f FAN\n", float64(*gasFee)/1000000.0)
	fmt.Printf("  时间戳：    %d\n", tx.Timestamp)
	fmt.Printf("  交易哈希：  %s\n", txHash)
	fmt.Println()

	// 如果指定了输出文件，仅保存不发送
	if *output != "" {
		txJSON, err := json.MarshalIndent(tx, "", "  ")
		if err != nil {
			log.Fatalf("序列化交易失败: %v", err)
		}

		if err := os.WriteFile(*output, txJSON, 0644); err != nil {
			log.Fatalf("保存交易失败: %v", err)
		}

		fmt.Printf("✓ 交易已保存到: %s\n", *output)
		fmt.Println("使用以下命令发送交易：")
		fmt.Printf("  curl -X POST -H \"Content-Type: application/json\" -d @%s %s/transaction\n", *output, *nodeURL)
		return
	}

	// 发送交易到节点
	fmt.Printf("正在发送交易到节点: %s\n", *nodeURL)
	if err := sendTransaction(tx, *nodeURL); err != nil {
		log.Fatalf("发送交易失败: %v", err)
	}

	fmt.Println()
	fmt.Println("✓ 交易发送成功！")
	fmt.Printf("交易哈希: %s\n", txHash)
	fmt.Println()
	fmt.Println("查询交易状态：")
	fmt.Printf("  curl %s/transaction/%s\n", *nodeURL, txHash)
	fmt.Println()
	fmt.Println("⚠️  注意：节点会自动检测重复交易哈希，请勿重复提交")
}

// 获取签名数据（与core.Transaction.SignData()保持一致）
// 注意：nonce不参与签名，由节点自动分配
func getSignData(tx *Transaction) []byte {
	buf := new(bytes.Buffer)

	buf.WriteByte(byte(tx.Type))
	buf.WriteString(tx.From)
	buf.WriteString(tx.To)
	buf.Write(uint64ToBytes(tx.Amount))
	buf.Write(uint64ToBytes(tx.GasFee))
	// Nonce不参与签名 - 由节点自动分配
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

	// 反序列化私钥
	var priv mldsa65.PrivateKey
	if err := priv.UnmarshalBinary(privateKeyBytes); err != nil {
		return nil, fmt.Errorf("私钥格式错误 (长度=%d): %v", len(privateKeyBytes), err)
	}

	// ML-DSA-65确定性签名
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
	// 序列化交易
	txJSON, err := json.Marshal(tx)
	if err != nil {
		return fmt.Errorf("序列化交易失败: %v", err)
	}

	// 发送POST请求
	url := nodeURL + "/transaction"
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(txJSON))
	if err != nil {
		return fmt.Errorf("HTTP请求失败: %v", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %v", err)
	}

	// 检查状态码
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("节点返回错误 (状态码=%d): %s", resp.StatusCode, string(body))
	}

	// 解析响应
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		// 如果不是JSON，直接显示
		fmt.Printf("节点响应: %s\n", string(body))
		return nil
	}

	// 显示响应
	if msg, ok := result["message"].(string); ok {
		fmt.Printf("节点响应: %s\n", msg)
	}
	if hash, ok := result["hash"].(string); ok {
		fmt.Printf("交易哈希: %s\n", hash)
	}

	return nil
}
