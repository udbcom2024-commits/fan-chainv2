package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
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

// SmartTransfer 智能转账工具（支持连续发送多笔交易）
func main() {
	// 命令行参数
	fromAddr := flag.String("from", "", "发送地址")
	toAddr := flag.String("to", "", "接收地址")
	amount := flag.Uint64("amount", 0, "转账金额（FAN）")
	keyFile := flag.String("key", "", "私钥文件路径")
	pubKeyFile := flag.String("pub", "", "公钥文件路径")
	nodeURL := flag.String("node", "http://localhost:9000", "节点URL")

	// 高级选项
	count := flag.Int("count", 1, "连续发送交易数量（默认1）")
	autoNonce := flag.Bool("auto-nonce", true, "自动管理nonce（默认true）")
	resetCache := flag.Bool("reset-cache", false, "重置nonce缓存")
	showStatus := flag.Bool("status", false, "显示nonce状态")

	flag.Parse()

	// 创建nonce管理器
	nm := NewNonceManager(*nodeURL)

	// 处理特殊命令
	if *resetCache {
		nm.ClearAllCache()
		fmt.Println("✓ Nonce缓存已清除")
		return
	}

	if *showStatus {
		if *fromAddr == "" {
			log.Fatal("请使用 -from 指定地址")
		}
		nm.ShowStatus(*fromAddr)
		return
	}

	// 验证参数
	if *fromAddr == "" || *toAddr == "" || *amount == 0 {
		fmt.Println("FAN链智能转账工具")
		fmt.Println("==============")
		fmt.Println()
		fmt.Println("用法:")
		fmt.Println("  transfer_smart -from <地址> -to <地址> -amount <金额> -key <私钥> -pub <公钥>")
		fmt.Println()
		fmt.Println("高级选项:")
		fmt.Println("  -count 5          连续发送5笔交易")
		fmt.Println("  -auto-nonce=false 禁用自动nonce管理")
		fmt.Println("  -reset-cache      重置nonce缓存")
		fmt.Println("  -status           显示nonce状态")
		fmt.Println()
		fmt.Println("示例:")
		fmt.Println("  # 单笔转账")
		fmt.Println("  transfer_smart -from Fxxx -to Fyyy -amount 100 -key priv.key -pub pub.key")
		fmt.Println()
		fmt.Println("  # 连续发送3笔转账（无需等待确认）")
		fmt.Println("  transfer_smart -from Fxxx -to Fyyy -amount 10 -key priv.key -pub pub.key -count 3")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// 读取密钥
	privKeyBytes, err := os.ReadFile(*keyFile)
	if err != nil {
		log.Fatalf("读取私钥失败: %v", err)
	}

	pubKeyBytes, err := os.ReadFile(*pubKeyFile)
	if err != nil {
		log.Fatalf("读取公钥失败: %v", err)
	}

	fmt.Println("FAN链智能转账工具")
	fmt.Println("==============")
	fmt.Println()

	// 如果启用自动nonce，显示状态
	if *autoNonce {
		nm.ShowStatus(*fromAddr)
	}

	// 连续发送交易
	successCount := 0
	for i := 0; i < *count; i++ {
		var nonce uint64

		if *autoNonce {
			// 智能nonce管理
			nonce, err = nm.GetNextNonce(*fromAddr)
			if err != nil {
				log.Printf("❌ 交易 #%d: 获取nonce失败: %v", i+1, err)
				continue
			}
		} else {
			// 手动查询nonce
			nonce, err = queryAccountNonce(*nodeURL, *fromAddr)
			if err != nil {
				log.Printf("❌ 交易 #%d: 查询nonce失败: %v", i+1, err)
				continue
			}
		}

		// 创建交易
		tx := &Transaction{
			Type:      TxTransfer,
			From:      *fromAddr,
			To:        *toAddr,
			Amount:    *amount * 1000000, // 转换为最小单位
			GasFee:    1,                  // 固定手续费
			Nonce:     nonce,
			Timestamp: getCurrentTimestamp(),
			Data:      []byte{},
		}

		// 签名交易
		signature, err := signTransaction(tx, privKeyBytes)
		if err != nil {
			log.Printf("❌ 交易 #%d: 签名失败: %v", i+1, err)
			continue
		}

		tx.Signature = signature
		tx.PublicKey = pubKeyBytes

		// 计算交易哈希
		tx.ID = calculateTxHash(tx)

		// 发送交易
		txHash, err := submitTransaction(*nodeURL, tx)
		if err != nil {
			log.Printf("❌ 交易 #%d: 提交失败: %v", i+1, err)

			// 如果是nonce错误，重置缓存
			if *autoNonce {
				nm.ResetNonce(*fromAddr)
			}
			continue
		}

		successCount++
		fmt.Printf("✓ 交易 #%d 已提交\n", i+1)
		fmt.Printf("  From: %s\n", *fromAddr)
		fmt.Printf("  To: %s\n", *toAddr)
		fmt.Printf("  Amount: %d FAN\n", *amount)
		fmt.Printf("  Nonce: %d\n", nonce)
		fmt.Printf("  Hash: %s\n", txHash)
		fmt.Printf("\n")
	}

	// 总结
	fmt.Printf("=================\n")
	fmt.Printf("总计: %d笔交易, 成功: %d笔, 失败: %d笔\n", *count, successCount, *count-successCount)

	if *autoNonce && successCount > 0 {
		fmt.Printf("\n提示: %d笔交易正在pending，等待确认后可运行:\n", nm.GetPendingCount(*fromAddr))
		fmt.Printf("  transfer_smart -from %s -status\n", *fromAddr)
		fmt.Printf("查看状态或使用 -reset-cache 清除缓存\n")
	}
}

// Transaction 交易结构（简化版）
type Transaction struct {
	Type      uint8  `json:"type"`
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    uint64 `json:"amount"`
	GasFee    uint64 `json:"gas_fee"`
	Nonce     uint64 `json:"nonce"`
	Timestamp int64  `json:"timestamp"`
	Data      []byte `json:"data,omitempty"`
	Signature []byte `json:"signature"`
	PublicKey []byte `json:"public_key"`
	ID        string `json:"id,omitempty"`
}

const (
	TxTransfer = 0
)

func getCurrentTimestamp() int64 {
	return time.Now().Unix()
}

// signTransaction 签名交易
func signTransaction(tx *Transaction, privateKeyBytes []byte) ([]byte, error) {
	message := serializeForSigning(tx)

	var priv mldsa65.PrivateKey
	if err := priv.UnmarshalBinary(privateKeyBytes); err != nil {
		return nil, fmt.Errorf("私钥格式错误: %v", err)
	}

	signature, err := priv.Sign(rand.Reader, message, crypto.Hash(0))
	if err != nil {
		return nil, fmt.Errorf("签名失败: %v", err)
	}
	return signature, nil
}

// serializeForSigning 序列化交易用于签名
func serializeForSigning(tx *Transaction) []byte {
	var buf bytes.Buffer
	buf.WriteByte(tx.Type)
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

// calculateTxHash 计算交易哈希
func calculateTxHash(tx *Transaction) string {
	data := serializeForSigning(tx)
	data = append(data, tx.Signature...)
	data = append(data, tx.PublicKey...)
	hash := sha3.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// submitTransaction 提交交易到节点
func submitTransaction(nodeURL string, tx *Transaction) (string, error) {
	data, err := json.Marshal(tx)
	if err != nil {
		return "", fmt.Errorf("序列化交易失败: %v", err)
	}

	url := fmt.Sprintf("%s/transaction", nodeURL)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("HTTP请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("节点返回错误 (状态码=%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("解析响应失败: %v", err)
	}

	if hash, ok := result["hash"].(string); ok {
		return hash, nil
	}

	return "", fmt.Errorf("响应中没有交易哈希")
}

// queryAccountNonce 查询账户nonce（回退方案）
func queryAccountNonce(nodeURL, address string) (uint64, error) {
	url := fmt.Sprintf("%s/balance/%s", nodeURL, address)
	resp, err := http.Get(url)
	if err != nil {
		return 0, fmt.Errorf("HTTP请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("节点返回错误 (状态码=%d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("读取响应失败: %v", err)
	}

	var result struct {
		Nonce uint64 `json:"nonce"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("解析JSON失败: %v", err)
	}

	return result.Nonce, nil
}

func uint64ToBytes(n uint64) []byte {
	buf := make([]byte, 8)
	for i := uint(0); i < 8; i++ {
		buf[7-i] = byte(n >> (i * 8))
	}
	return buf
}
