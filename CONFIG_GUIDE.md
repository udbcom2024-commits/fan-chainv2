# FAN链配置文件填写指南

## 概述

FAN链使用两个主要配置文件：
- `config.json` - 节点运行配置（每个节点独立）
- `consensus.json` - 共识参数配置（全网必须一致）

---

## ⚠️ 分叉处理流程（重要）

当发现节点与网络发生分叉时，必须立即处理。分叉的典型症状：

**检测方法**：
```bash
# 同时检查所有节点高度
curl -s http://38.34.185.113:9000/status | jq .height  # Node2
curl -s http://35.240.142.148:9000/status | jq .height  # Node3
curl -s http://34.92.124.221:9000/status | jq .height   # Node4
```

**分叉症状**：
- 两个验证者节点高度相差较大（>30块）且各自独立增长
- 全节点卡在某高度不再同步
- 日志中出现 "FORK DETECTED" 或 block hash 不匹配

**标准处理流程**：

### 步骤1: 确定权威节点
选择链最长且状态正确的节点作为权威节点（通常是高度最高的验证者）。

### 步骤2: 停止分叉节点
```bash
# 停止分叉的节点（以Node2为例）
ssh -i ~/.ssh/fan_node2_key root@38.34.185.113 "pkill -f fan-chain"

# 停止全节点（如果卡住）
ssh -i ~/.ssh/fan_node4_hk udbcom2024@34.92.124.221 "pkill -f fan-chain"
```

### 步骤3: 清理数据目录
```bash
# 删除分叉节点的数据（保留配置和密钥）
ssh -i ~/.ssh/fan_node2_key root@38.34.185.113 "rm -rf ~/fan-chain/data/*"
ssh -i ~/.ssh/fan_node4_hk udbcom2024@34.92.124.221 "rm -rf ~/fan-chain/data/*"
```

### 步骤4: 重启节点同步
```bash
# 重启节点，自动从权威节点同步
ssh -i ~/.ssh/fan_node2_key root@38.34.185.113 "cd ~/fan-chain && nohup ./fan-chain > output.log 2>&1 &"
ssh -i ~/.ssh/fan_node4_hk udbcom2024@34.92.124.221 "cd ~/fan-chain && nohup ./fan-chain > output.log 2>&1 &"
```

### 步骤5: 验证同步状态
```bash
# 观察2分钟，确认所有节点高度一致
for i in {1..6}; do
  echo "=== Check $i/6 ==="
  curl -s http://38.34.185.113:9000/status | jq '{node2: .height}'
  curl -s http://35.240.142.148:9000/status | jq '{node3: .height}'
  curl -s http://34.92.124.221:9000/status | jq '{node4: .height}'
  sleep 20
done
```

**预防措施**：
- 验证者激活前会检查block hash，若与网络不一致则拒绝激活
- 高度容忍最多12块差异（一个checkpoint周期）
- 真正的安全保障是区块哈希验证，而非严格的高度同步

---

## 1. config.json - 节点运行配置（简化版）

节点配置非常简单，只需要3项核心配置：

### 最小配置示例

```json
{
  "node_type": "validator",
  "address": "F4ubbrtkdkaowdfykes1e13rflnce3gi65wcz",
  "seed_peers": ["35.240.142.148:9001"]
}
```

### 配置项说明

| 配置项 | 必填 | 说明 |
|--------|------|------|
| address | 是 | 节点地址（41字符，F开头） |
| seed_peers | 是 | 种子节点列表（至少1个） |
| node_type | 否 | validator/regular，默认validator |
| api_port | 否 | API端口，默认9000 |
| p2p_port | 否 | P2P端口，默认9001 |

### 密钥文件位置

```
~/fan-chain/
├── keys/
│   ├── node_private.key   # 私钥文件（必须）
│   └── node_public.key    # 公钥文件（必须）
├── config.json            # 节点配置
└── consensus.json         # 共识配置（从种子节点自动同步）
```

**重要**：address必须与keys目录下的密钥对匹配！

---

## 2. config.json - 完整配置参考

### 配置示例

```json
{
  "node_type": "validator",
  "address": "F1r06tlcaoiegfl7w6d1b84g88njhkd3wq57x",
  "api_port": 9000,
  "p2p_port": 9001,
  "seed_peers": [
    "35.240.142.148:9001"
  ]
}
```

### 参数说明

#### node_type（节点类型）
- **类型**: string
- **必填**: 是
- **可选值**:
  - `"validator"` - 验证者节点（参与共识出块）
  - `"regular"` - 普通节点（仅同步区块）
- **说明**: 决定节点在网络中的角色

#### address（节点地址）
- **类型**: string
- **必填**: 是（validator节点必填）
- **格式**: 以"F"开头的41字符地址
- **示例**: `"F1r06tlcaoiegfl7w6d1b84g88njhkd3wq57x"`
- **说明**:
  - 验证者节点必须配置地址用于接收奖励
  - 地址对应的私钥必须存放在 `keys/node_private.key`

#### api_port（API端口）
- **类型**: number
- **默认值**: 9000
- **说明**: HTTP API服务端口（基础功能）

#### p2p_port（P2P端口）
- **类型**: number
- **默认值**: 9001
- **说明**: P2P网络通信端口

#### seed_peers（种子节点列表）
- **类型**: array of string
- **格式**: `["host:port"]`
- **示例**:
  ```json
  [
    "35.240.142.148:9001"
  ]
  ```
- **说明**: 启动时连接的种子节点（通常是Node1）

---

## 2. consensus.json - 共识参数配置

⚠️ **重要**: `consensus.json` 必须与其他节点完全一致！

### 关键点

1. **不要手动修改**: 共识参数由全网协商后统一更新
2. **定期同步**: 从Node1或官方仓库获取最新版本
3. **验证哈希**: 启动后检查日志中的 `consensus_hash` 是否匹配

### 当前版本

- **版本**: 1.1.0
- **哈希**: `d79e118637de13ec...`

### 参数分类（只读参考）

1. **chain_params**: 链基础参数（代币单位、创世地址等）
2. **block_params**: 出块间隔、Checkpoint配置
3. **economic_params**: Gas费、出块奖励、质押要求
4. **validator_params**: 验证者相关配置
5. **transaction_params**: 交易大小、Data字段限制
6. **network_params**: P2P网络参数
7. **security_params**: 安全和惩罚机制
8. **storage_params**: 数据存储策略
9. **reward_thresholds**: 奖励减半阈值

> 详细参数说明请参考Node1的完整配置文档

---

## 3. 配置文件位置

### 标准目录结构

```
/root/fan-chain/
├── config.json          # 节点配置
├── consensus.json       # 共识配置（从Node1同步）
├── keys/                # 软链接到 /root/keys/
│   └── node_private.key
├── data/                # 区块数据（自动生成）
└── fan-chain           # 可执行文件
```

---

## 4. 节点启动流程

### 首次启动

```bash
cd /root/fan-chain

# 1. 确认配置文件存在
ls -la config.json consensus.json

# 2. 确认私钥存在
ls -la keys/node_private.key

# 3. 启动节点
nohup ./fan-chain -config config.json > fan.log 2>&1 &

# 4. 查看启动日志
tail -f fan.log
```

### 验证启动成功

检查日志中是否有以下信息：

```
✅ 共识配置加载成功
   版本: 1.1.0
   哈希: d79e118637de13ec...

✅ Checkpoint request sent
```

如果看到 "consensus: OK"，说明与网络连接成功。

---

## 5. 日常运维

### 查看节点状态

```bash
# 检查进程是否运行
pgrep -af fan-chain

# 查看最新日志
tail -30 /root/fan-chain/fan.log

# 查看区块高度
curl -s http://localhost:9000/api/status
```

### 重启节点

```bash
# 停止节点
pkill -9 fan-chain

# 等待2秒
sleep 2

# 重新启动
cd /root/fan-chain
nohup ./fan-chain -config config.json > fan.log 2>&1 &
```

### 清理重新同步

```bash
# 停止节点
pkill -9 fan-chain

# 删除区块数据（保留配置和密钥）
rm -rf /root/fan-chain/data

# 重新启动（将从头同步）
cd /root/fan-chain
nohup ./fan-chain -config config.json > fan.log 2>&1 &
```

---

## 6. 配置更新流程

### 更新 consensus.json

```bash
# 1. 停止节点
pkill -9 fan-chain

# 2. 备份旧配置
cp /root/fan-chain/consensus.json /root/consensus_v1.1.0_backup.json

# 3. 从Node1复制新配置（或通过scp上传）
# 方式1: 如果本地有新文件
# scp -i ~/.ssh/fan_node2_key consensus.json root@38.34.185.113:/root/fan-chain/

# 4. 重启节点
cd /root/fan-chain
nohup ./fan-chain -config config.json > fan.log 2>&1 &

# 5. 验证新配置
tail -30 fan.log | grep "共识配置"
```

---

## 7. 故障排查

### 节点无法启动

**检查清单**:
1. 配置文件是否存在: `ls config.json consensus.json`
2. 私钥是否存在: `ls keys/node_private.key`
3. 端口是否被占用: `netstat -tulpn | grep -E '9000|9001'`
4. 查看错误日志: `tail -50 fan.log`

### 无法连接到种子节点

**解决方法**:
1. 检查seed_peers配置是否正确
2. 测试网络连通性: `telnet 35.240.142.148 9001`
3. 检查防火墙规则
4. 查看日志: `grep -i "peer\|connect" fan.log`

### 共识哈希不匹配

**错误信息**: "consensus mismatch" 或被网络拒绝

**解决方法**:
1. 从Node1获取最新的 `consensus.json`
2. 替换本地文件
3. 重启节点

### 同步停滞

**检查**:
```bash
# 查看当前高度
curl -s http://localhost:9000/api/status

# 查看是否有同步日志
tail -30 fan.log | grep -i sync
```

**解决方法**:
1. 检查网络连接
2. 重启节点
3. 如果问题持续，清理数据重新同步

---

## 8. 监控建议

### 定期检查项

每天检查：
- [ ] 节点进程是否运行: `pgrep fan-chain`
- [ ] 区块高度是否增长: `curl localhost:9000/api/status`
- [ ] 日志是否有ERROR: `grep ERROR fan.log`

每周检查：
- [ ] 磁盘空间: `df -h`
- [ ] 数据目录大小: `du -sh data/`
- [ ] 日志文件大小: `ls -lh fan.log`

---

## 9. 安全注意事项

1. **保护私钥**: `/root/keys/node_private.key` 必须安全保管
2. **备份配置**: 定期备份 `config.json` 和 `consensus.json`
3. **限制访问**: API端口(9000)不要暴露到公网
4. **监控日志**: 定期检查异常连接和错误日志

---

## 10. 常见问题

### Q: 为什么没有Web界面？
**A**: Node2是简化版，专注于核心区块链功能。Web界面在Node1上。

### Q: 如何查看区块数据？
**A**: 使用API接口:
```bash
# 查看状态
curl http://localhost:9000/api/status

# 查看特定区块（如果API支持）
curl http://localhost:9000/api/block/100
```

### Q: consensus.json什么时候需要更新？
**A**: 当全网进行共识升级时，会提前通知。务必在指定时间同时更新所有节点。

### Q: 如何知道节点正常运行？
**A**: 检查3点:
1. 进程运行: `pgrep fan-chain` 有输出
2. 区块增长: `curl localhost:9000/api/status` 高度递增
3. 日志正常: `tail fan.log` 看到出块或同步日志

---

## 11. 联系支持

遇到问题时：
1. 收集错误日志: `tail -100 fan.log > error.log`
2. 记录节点配置（隐藏私密信息）
3. 联系Node1运维人员

---

## 附录：快速参考

### 常用命令

```bash
# 启动
cd /root/fan-chain && nohup ./fan-chain -config config.json > fan.log 2>&1 &

# 停止
pkill -9 fan-chain

# 查看日志
tail -f /root/fan-chain/fan.log

# 查看状态
curl http://localhost:9000/api/status

# 检查进程
pgrep -af fan-chain
```

### 目录位置

- 项目目录: `/root/fan-chain/`
- 私钥目录: `/root/keys/` (软链接)
- 数据目录: `/root/fan-chain/data/`
- 日志文件: `/root/fan-chain/fan.log`
