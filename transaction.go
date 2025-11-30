package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"fan-chain/core"
)

func (n *Node) SubmitTransaction(tx *core.Transaction) error {
	// ============ 【架构约束】非验证者节点不处理交易 ============
	// 原因：
	// 1. 交易池只应由验证者节点维护
	// 2. 客户端应直接提交交易到验证者节点
	// 3. 非验证者节点不参与交易打包，无需维护交易池
	if !n.isActiveValidator(n.address) {
		log.Printf("❌ [FULL NODE] Transaction rejected: non-validator nodes do not accept transactions")
		return fmt.Errorf("this node is not a validator and cannot accept transactions, please submit to a validator node")
	}

	// ============ 1. 基于哈希的去重检查（最高优先级）============
	// 这是防止重复提交的第一道防线
	txHash := tx.Hash()
	filename := filepath.Join(n.pendingTxDir, "tx_"+fmt.Sprintf("%x", txHash.Bytes()[:8])+".json")

	if _, err := os.Stat(filename); err == nil {
		// 交易已存在于pending池，直接拒绝
		log.Printf("⚠️  [TX_POOL] Transaction already exists in pool: %x", txHash.Bytes()[:8])
		return fmt.Errorf("transaction already exists in pool (duplicate hash)")
	}

	// ============ 2. Nonce 检查和分配 ============
	// 跳过系统交易（奖励/惩罚）的nonce检查
	if tx.Type != core.TxReward && tx.Type != core.TxSlash {
		// 用户不应该指定nonce，如果指定了则拒绝（nonce必须为0）
		if tx.Nonce != 0 {
			return fmt.Errorf("user should not specify nonce (got %d), nonce must be 0 and will be auto-assigned by node", tx.Nonce)
		}

		// 验证者节点：自动计算并分配nonce
		currentNonce, err := n.state.GetNonce(tx.From)
		if err != nil {
			return fmt.Errorf("failed to get nonce for %s: %v", tx.From, err)
		}

		// 检查pending池中该账户已有的交易数量
		pendingCount := n.countPendingTransactions(tx.From)

		// 分配nonce = 当前nonce + pending池中的交易数
		tx.Nonce = currentNonce + uint64(pendingCount)

		log.Printf("🔄 [VALIDATOR] Auto-assigned nonce for %s: current=%d, pending=%d, assigned=%d",
			tx.From[:10], currentNonce, pendingCount, tx.Nonce)
	}

	// ============ 3. 交易格式和签名验证 ============
	// 验证交易格式（严格时间戳验证）
	if err := tx.Validate(false); err != nil {
		return fmt.Errorf("transaction validation failed: %v", err)
	}

	// 验证签名
	if err := tx.VerifySignature(); err != nil {
		return fmt.Errorf("signature verification failed: %v", err)
	}

	// ============ 4. 入池（不广播）============
	// 序列化交易
	txJSON, err := json.Marshal(tx)
	if err != nil {
		return fmt.Errorf("serialize transaction failed: %v", err)
	}

	// 保存到pending目录
	if err := os.WriteFile(filename, txJSON, 0644); err != nil {
		return fmt.Errorf("write transaction file failed: %v", err)
	}

	log.Printf("✓ [TX_POOL] Transaction accepted: %x", txHash.Bytes()[:8])

	// 【架构变更】验证者节点不广播交易
	// 原因：客户端应该直接提交到验证者，而不是依赖P2P广播
	// 如果需要多验证者冗余，客户端可以同时提交到多个验证者

	return nil
}

// countPendingTransactions 统计pending池中指定地址的交易数量
func (n *Node) countPendingTransactions(address string) int {
	files, err := os.ReadDir(n.pendingTxDir)
	if err != nil {
		return 0
	}

	count := 0
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		data, err := os.ReadFile(filepath.Join(n.pendingTxDir, file.Name()))
		if err != nil {
			continue
		}

		var tx core.Transaction
		if err := json.Unmarshal(data, &tx); err != nil {
			continue
		}

		// 统计来自该地址的交易
		if tx.From == address && tx.Type != core.TxReward && tx.Type != core.TxSlash {
			count++
		}
	}

	return count
}

// HandleReceivedTransaction 处理从P2P网络接收到的交易
// 【架构约束】此函数已废弃，因为交易不应通过P2P广播
// 客户端应直接提交交易到验证者节点，而不是通过P2P网络传播
func (n *Node) HandleReceivedTransaction(tx *core.Transaction) error {
	log.Printf("⚠️  [DEPRECATED] Received transaction via P2P (this should not happen in current architecture)")
	return fmt.Errorf("transaction broadcasting via P2P is deprecated, clients should submit directly to validators")
}

func (n *Node) validateAndDeduplicateTransactions(txs []*core.Transaction) []*core.Transaction {
	if len(txs) == 0 {
		return txs
	}

	log.Printf("[TX_VALIDATE] Processing %d pending transactions", len(txs))

	txsByAccount := make(map[string][]*core.Transaction)
	for _, tx := range txs {
		if tx.Type == core.TxReward || tx.Type == core.TxSlash {
			continue
		}
		txsByAccount[tx.From] = append(txsByAccount[tx.From], tx)
		log.Printf("[TX_VALIDATE] Tx from %s: nonce=%d, amount=%d", tx.From[:10], tx.Nonce, tx.Amount)
	}

	validTxs := make([]*core.Transaction, 0)

	for address, accountTxs := range txsByAccount {
		currentNonce, err := n.state.GetNonce(address)
		if err != nil {
			log.Printf("[TX_VALIDATE] Failed to get nonce for %s: %v", address[:10], err)
			continue
		}

		log.Printf("[TX_VALIDATE] Account %s has %d txs, current nonce=%d", address[:10], len(accountTxs), currentNonce)

		seenNonces := make(map[uint64]bool)
		for _, tx := range accountTxs {
			if seenNonces[tx.Nonce] {
				log.Printf("[TX_VALIDATE] SKIP tx (duplicate nonce): nonce=%d", tx.Nonce)
				continue
			}

			if tx.Nonce != currentNonce {
				log.Printf("[TX_VALIDATE] SKIP tx (nonce mismatch): tx.nonce=%d != current=%d", tx.Nonce, currentNonce)
				continue
			}

			// 根据交易类型检查不同的余额
			switch tx.Type {
			case core.TxTransfer:
				// 转账：检查可用余额
				balance, err := n.state.GetBalance(address)
				if err != nil {
					log.Printf("[TX_VALIDATE] SKIP tx (failed to get balance): %v", err)
					continue
				}
				totalCost := tx.Amount + tx.GasFee
				if balance < totalCost {
					log.Printf("[TX_VALIDATE] SKIP tx (insufficient balance): balance=%d < cost=%d", balance, totalCost)
					continue
				}

			case core.TxStake:
				// 质押：检查可用余额
				balance, err := n.state.GetBalance(address)
				if err != nil {
					log.Printf("[TX_VALIDATE] SKIP tx (failed to get balance): %v", err)
					continue
				}
				if balance < tx.Amount {
					log.Printf("[TX_VALIDATE] SKIP tx (insufficient balance for stake): balance=%d < amount=%d", balance, tx.Amount)
					continue
				}

			case core.TxUnstake:
				// 解押：检查质押余额
				account, err := n.state.GetAccount(address)
				if err != nil {
					log.Printf("[TX_VALIDATE] SKIP tx (failed to get account): %v", err)
					continue
				}
				if account.StakedBalance < tx.Amount {
					log.Printf("[TX_VALIDATE] SKIP tx (insufficient staked balance): staked=%d < amount=%d", account.StakedBalance, tx.Amount)
					continue
				}
			}

			log.Printf("[TX_VALIDATE] ✓ ACCEPT tx: nonce=%d, amount=%d", tx.Nonce, tx.Amount)
			seenNonces[tx.Nonce] = true
			validTxs = append(validTxs, tx)
			currentNonce++
		}
	}

	for _, tx := range txs {
		if tx.Type == core.TxReward || tx.Type == core.TxSlash {
			validTxs = append(validTxs, tx)
		}
	}

	return validTxs
}
