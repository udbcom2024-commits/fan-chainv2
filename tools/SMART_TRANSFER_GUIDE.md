# FAN链智能转账工具使用指南

## 概述

`transfer_smart.exe` 是FAN链的**智能转账工具**，采用类似MetaMask的nonce管理机制，支持**连续发送多笔交易而无需等待确认**。

## 核心优势

### 对比旧工具

| 特性 | 旧transfer.exe | 新transfer_smart.exe |
|------|---------------|---------------------|
| Nonce管理 | ❌ 每次查询链上 | ✅ 智能本地缓存 |
| 连续交易 | ❌ 必须等待确认 | ✅ 立即发送多笔 |
| 用户体验 | ⏳ 慢速顺序操作 | ⚡ 快速批量操作 |
| 类似钱包 | - | MetaMask / Trust Wallet |

### 实际效果

```bash
# 旧方式：发送3笔交易需要15-30秒
transfer.exe -amount 10 ...  # 等待5秒确认
transfer.exe -amount 10 ...  # 等待5秒确认
transfer.exe -amount 10 ...  # 等待5秒确认

# 新方式：发送3笔交易只需1-2秒
transfer_smart.exe -amount 10 -count 3 ...  # ⚡ 立即完成
```

## 基本用法

### 1. 单笔转账

```bash
transfer_smart.exe -from <发送地址> -to <接收地址> -amount <金额> \
  -key <私钥文件> -pub <公钥文件>
```

**示例**：
```bash
transfer_smart.exe \
  -from F0rkjuwww2dtoocnd42h9e8uaoxpzgptgoe10 \
  -to F7biz3m3dfq966u5vqj2wcts4z21nmdbalsyy \
  -amount 100 \
  -key ../addr/genesis/genesis_private.key \
  -pub ../addr/genesis/genesis_public.key
```

输出：
```
=== Nonce状态 ===
地址: F0rkjuwww2dtoocnd42h9e8uaoxpzgptgoe10
链上已确认nonce: 5
本地无pending交易
下一个可用nonce: 5
===============

✓ 交易 #1 已提交
  From: F0rkjuwww2dtoocnd42h9e8uaoxpzgptgoe10
  To: F7biz3m3dfq966u5vqj2wcts4z21nmdbalsyy
  Amount: 100 FAN
  Nonce: 5
  Hash: a1b2c3d4...
```

### 2. 连续发送多笔交易（核心功能）

```bash
transfer_smart.exe -from <地址> -to <地址> -amount <金额> \
  -key <私钥> -pub <公钥> -count 5
```

**示例：连续发送5笔10 FAN转账**
```bash
transfer_smart.exe \
  -from F0rkjuwww2dtoocnd42h9e8uaoxpzgptgoe10 \
  -to F7biz3m3dfq966u5vqj2wcts4z21nmdbalsyy \
  -amount 10 \
  -key ../addr/genesis/genesis_private.key \
  -pub ../addr/genesis/genesis_public.key \
  -count 5
```

输出：
```
=== Nonce状态 ===
地址: F0rkjuwww2dtoocnd42h9e8uaoxpzgptgoe10
链上已确认nonce: 5
本地无pending交易
下一个可用nonce: 5
===============

✓ 交易 #1 已提交
  Nonce: 5
  Hash: a1b2c3d4...

✓ 交易 #2 已提交
  Nonce: 6
  Hash: e5f6g7h8...

✓ 交易 #3 已提交
  Nonce: 7
  Hash: i9j0k1l2...

✓ 交易 #4 已提交
  Nonce: 8
  Hash: m3n4o5p6...

✓ 交易 #5 已提交
  Nonce: 9
  Hash: q7r8s9t0...

=================
总计: 5笔交易, 成功: 5笔, 失败: 0笔

提示: 5笔交易正在pending，等待确认后可运行:
  transfer_smart.exe -from F0rkjuwww2dtoocnd42h9e8uaoxpzgptgoe10 -status
```

## 高级功能

### 3. 查看Nonce状态

```bash
transfer_smart.exe -from <地址> -status
```

**示例**：
```bash
transfer_smart.exe -from F0rkjuwww2dtoocnd42h9e8uaoxpzgptgoe10 -status
```

输出：
```
=== Nonce状态 ===
地址: F0rkjuwww2dtoocnd42h9e8uaoxpzgptgoe10
链上已确认nonce: 5
本地pending最高nonce: 9
未确认交易数: 5
下一个可用nonce: 10
===============
```

### 4. 重置Nonce缓存

```bash
transfer_smart.exe -reset-cache
```

**使用场景**：
- 交易已全部确认，清除本地缓存
- Nonce出现错误，需要重新同步
- 切换到新账户

### 5. 禁用自动Nonce管理（回退到旧模式）

```bash
transfer_smart.exe -from <地址> -to <地址> -amount <金额> \
  -key <私钥> -pub <公钥> -auto-nonce=false
```

## 工作原理

### Nonce智能管理（类似MetaMask）

```
1. 查询链上确认的nonce (confirmedNonce = 5)
2. 检查本地pending的最高nonce (localPendingNonce = 7)
3. 选择更大的nonce + 1:
   - 如果localPendingNonce >= confirmedNonce:
     nextNonce = localPendingNonce + 1  (= 8)
   - 否则:
     nextNonce = confirmedNonce  (= 5)
4. 保存到本地缓存文件：~/.fan_nonce_cache.json
```

### 本地缓存文件

位置：`~/.fan_nonce_cache.json`

内容示例：
```json
{
  "pending_nonces": {
    "F0rkjuwww2dtoocnd42h9e8uaoxpzgptgoe10": 9,
    "F7biz3m3dfq966u5vqj2wcts4z21nmdbalsyy": 3
  }
}
```

## 命令行参数完整列表

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `-from` | string | (必填) | 发送地址 |
| `-to` | string | (必填) | 接收地址 |
| `-amount` | uint64 | (必填) | 转账金额（FAN） |
| `-key` | string | (必填) | 私钥文件路径 |
| `-pub` | string | (必填) | 公钥文件路径 |
| `-node` | string | `http://localhost:9000` | 节点URL |
| `-count` | int | `1` | 连续发送交易数量 |
| `-auto-nonce` | bool | `true` | 启用自动nonce管理 |
| `-reset-cache` | bool | `false` | 重置nonce缓存 |
| `-status` | bool | `false` | 显示nonce状态 |

## 实际应用场景

### 场景1：批量发送奖励

```bash
# 向100个地址发送奖励，每个10 FAN
for address in $(cat reward_addresses.txt); do
  transfer_smart.exe \
    -from F0rkjuwww2dtoocnd42h9e8uaoxpzgptgoe10 \
    -to $address \
    -amount 10 \
    -key genesis_private.key \
    -pub genesis_public.key &
done
wait

# 所有交易并发提交，总耗时约5-10秒（而非500秒）
```

### 场景2：压力测试

```bash
# 向网络发送1000笔测试交易
transfer_smart.exe \
  -from F0rkjuwww2dtoocnd42h9e8uaoxpzgptgoe10 \
  -to F7biz3m3dfq966u5vqj2wcts4z21nmdbalsyy \
  -amount 0.000001 \
  -count 1000 \
  -key test_private.key \
  -pub test_public.key
```

### 场景3：定期支付

```bash
# 每小时自动支付工资（cron任务）
0 * * * * transfer_smart.exe \
  -from F0rkjuwww2dtoocnd42h9e8uaoxpzgptgoe10 \
  -to F7biz3m3dfq966u5vqj2wcts4z21nmdbalsyy \
  -amount 5 \
  -key ../keys/salary.key \
  -pub ../keys/salary_pub.key
```

## 安全提示

1. **私钥保护**：私钥文件权限应设为600（仅所有者可读）
   ```bash
   chmod 600 genesis_private.key
   ```

2. **缓存文件**：`~/.fan_nonce_cache.json` 不包含私钥，仅存储nonce状态

3. **并发限制**：建议单次最多发送100笔交易，避免网络拥堵

4. **错误恢复**：如果nonce出错，使用 `-reset-cache` 重新同步

## 故障排查

### 问题1：Nonce错误

**症状**：
```
❌ 交易提交失败: invalid nonce: expected 5, got 8
```

**解决**：
```bash
# 重置nonce缓存
transfer_smart.exe -reset-cache

# 或禁用自动nonce
transfer_smart.exe ... -auto-nonce=false
```

### 问题2：交易卡住

**症状**：交易长时间pending

**解决**：
```bash
# 1. 检查nonce状态
transfer_smart.exe -from <地址> -status

# 2. 查看链上确认情况
curl http://35.240.142.148:9000/balance/<地址>

# 3. 如果链上nonce已更新，重置缓存
transfer_smart.exe -reset-cache
```

## 与以太坊钱包对比

| 特性 | MetaMask | transfer_smart.exe |
|------|----------|-------------------|
| Nonce管理 | ✅ 自动 | ✅ 自动 |
| 本地缓存 | ✅ IndexedDB | ✅ JSON文件 |
| 连续交易 | ✅ 支持 | ✅ 支持 |
| 批量发送 | ❌ 需逐笔确认 | ✅ 一次命令 |
| 命令行 | ❌ | ✅ |

## 技术细节

### Nonce验证流程

```
┌─────────────────┐
│  用户发起交易    │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ 查询链上nonce=5  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ 读取缓存nonce=7  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ 选择max(5,7)+1=8│
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ 签名交易nonce=8  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ 提交到节点       │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ 保存nonce=8缓存  │
└─────────────────┘
```

## 更新日志

**v1.0.0** (2025-11-20)
- ✅ 实现智能nonce管理
- ✅ 支持连续发送多笔交易
- ✅ 本地缓存机制
- ✅ 兼容旧transfer.exe的所有功能

---

**开发者**: FAN链团队
**文档更新**: 2025-11-20
