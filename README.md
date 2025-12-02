# FAN Chain

FAN链 - 基于VRF共识的POS区块链

## 核心特性

- **POS共识**: VRF随机选择出块验证者
- **5秒出块**:5秒（1块）为一个Checkpoint周期
- **多签共识**: 1/2签名门槛确认Checkpoint
- **分叉解决**: "认准真大哥"规则 - 跟随最高Checkpoint
- **加密网络**: ML-KEM密钥交换的P2P加密通信

## 设计哲学（五兄弟家规）

| 规矩 | 说明 |
|------|------|
| 不许分叉 | 单链原则，只承认一条链 |
| 认准真大哥 | 谁的Checkpoint高度高谁就是真理 |
| 小弟要狠 | 发现分叉立即回滚跟随大哥 |
| 大哥要勇 | 即使孤立也要继续出块 |

## 核心参数

| 参数 | 值 |
|------|-----|
| 总供应 | 1,400,000,000 FAN |
| 1 FAN | 1,000,000 最小单位 |
| 出块间隔 | 5秒 |
| Checkpoint间隔 | 1块 (5秒) |
| 最低质押 | 1,000,000 FAN |
| 出块奖励 | 10 FAN |

## 快速开始

```bash
# 编译
go build -o fan-chain .

# 配置 (编辑 config.json)
{
  "node_name": "MyNode",
  "node_address": "<YOUR_ADDRESS>",
  "private_key_file": "./keys/node_private.key",
  "public_key_file": "./keys/node_public.key",
  "p2p_port": 9001,
  "api_port": 9000
}

# 生成密钥
cd tools && go run keygen.go generate -o ../keys -n node

# 启动
./fan-chain
```

## 目录结构

```
├── api/          # HTTP API
├── config/       # 配置加载
├── consensus/    # 共识逻辑
├── core/         # 核心类型（区块、交易、账户）
├── crypto/       # 加密（签名、ML-KEM）
├── network/      # P2P网络
├── state/        # 状态管理
├── storage/      # 数据存储
├── tools/        # 工具（keygen、stake、transfer）
└── web/          # 浏览器前端
```

## API端点

| 端点 | 说明 |
|------|------|
| GET /status | 节点状态 |
| GET /block/{height} | 获取区块 |
| GET /balance/{address} | 查询余额 |
| GET /transaction/{hash} | 查询交易 |
| POST /transaction | 提交交易 |

## 交易类型

| Type | 名称 | 手续费 |
|------|------|--------|
| 0 | 转账 | 1 |
| 1 | 质押 | 0 |
| 2 | 解押 | 0 |
| 3 | 奖励 | 0 |



浏览器: http://history.f-a-n.org

## 许可证

MIT License
