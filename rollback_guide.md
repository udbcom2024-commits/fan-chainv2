# Node2 回退操作指南

## 一、预防措施（已实施）

### 1. 自动备份机制
- **本地**：Windows 每分钟备份到 `C:\Users\jjj\fan\backups\node2_data\`
- **服务器**：Linux 每分钟备份到 `/root/fan-chain/backups/`
- 保留最近 10 个备份

### 2. P0 总量验证
- 出块前验证
- Checkpoint 生成前验证
- 同步时验证

## 二、问题识别

### 症状判断
```bash
# 检查总量
curl http://38.34.185.113:9000/verify_supply

# 查看最新区块
curl http://38.34.185.113:9000/latest

# 检查节点状态
curl http://38.34.185.113:9000/status
```

### 日志关键词
- `🚨 P0违反` - 总量错误
- `⛔ 停止出块` - 出块被阻止
- `failed to verify total supply` - 验证失败

## 三、回退操作步骤

### 方案 A：快速回退（推荐）

```bash
# 1. 立即停止节点
ssh root@38.34.185.113
pkill -f fan-chain

# 2. 备份当前错误状态（用于分析）
cd /root/fan-chain
mv data data_error_$(date +%Y%m%d_%H%M%S)

# 3. 恢复最近的正确备份
cp -r backups/backup_20251123_HHMMSS data

# 4. 重启节点
./fan-chain

# 5. 验证总量
curl http://localhost:9000/verify_supply
```

### 方案 B：从 Node1 重新同步

```bash
# 1. 停止 Node2
pkill -f fan-chain

# 2. 完全清空数据
rm -rf /root/fan-chain/data/*

# 3. 启动节点（会自动从 Node1 同步）
./fan-chain

# 4. 等待同步完成
# 查看日志确认：
tail -f output.log | grep "P0验证"
```

### 方案 C：手动选择备份点

```bash
# 1. 列出所有备份
ls -la /root/fan-chain/backups/

# 2. 查看备份信息
cat /root/fan-chain/backups/backup_YYYYMMDD_HHMMSS/backup_info.txt

# 3. 选择特定备份恢复
pkill -f fan-chain
rm -rf /root/fan-chain/data
cp -r /root/fan-chain/backups/backup_YYYYMMDD_HHMMSS /root/fan-chain/data
./fan-chain
```

## 四、Windows 本地回退

```powershell
# 1. 查看备份列表
Get-ChildItem C:\Users\jjj\fan\backups\node2_data\

# 2. 恢复特定备份
$backupPath = "C:\Users\jjj\fan\backups\node2_data\backup_20251123_HHMMSS"
Remove-Item C:\Users\jjj\fan\node2\data -Recurse -Force
Copy-Item $backupPath C:\Users\jjj\fan\node2\data -Recurse
```

## 五、紧急停止验证者功能

如果发现问题但不想停止节点：

### 1. 临时退出验证者
```bash
# SSH 到服务器
ssh root@38.34.185.113

# 发送解押交易（退出验证者状态）
curl -X POST http://localhost:9000/unstake \
  -d '{"amount": 1000000000000}'
```

### 2. 修改配置禁用出块
```bash
# 编辑配置文件
vi /root/fan-chain/config.json

# 添加或修改
"disable_block_production": true
```

## 六、验证恢复成功

### 检查清单
- [ ] 总量 = 1400000000000000
- [ ] 节点正常同步
- [ ] 区块高度递增
- [ ] 没有错误日志

### 验证命令
```bash
# 完整验证脚本
#!/bin/bash

echo "=== 节点恢复验证 ==="

# 1. 检查进程
if pgrep -f fan-chain > /dev/null; then
    echo "✅ 节点运行中"
else
    echo "❌ 节点未运行"
    exit 1
fi

# 2. 检查总量
SUPPLY=$(curl -s http://localhost:9000/total_supply)
if [ "$SUPPLY" = "1400000000000000" ]; then
    echo "✅ 总量正确: $SUPPLY"
else
    echo "❌ 总量错误: $SUPPLY"
    exit 1
fi

# 3. 检查同步
STATUS=$(curl -s http://localhost:9000/status)
echo "✅ 节点状态: $STATUS"

echo "=== 验证完成 ==="
```

## 七、预设回退触发器

### 自动回退脚本
```bash
#!/bin/bash
# auto_rollback.sh - 自动检测并回退

while true; do
    # 检查总量
    SUPPLY=$(curl -s http://localhost:9000/total_supply 2>/dev/null)

    if [ "$SUPPLY" != "1400000000000000" ] && [ -n "$SUPPLY" ]; then
        echo "🚨 检测到总量异常：$SUPPLY"
        echo "⏮️ 自动回退中..."

        # 停止节点
        pkill -f fan-chain

        # 恢复最新备份
        LATEST_BACKUP=$(ls -t /root/fan-chain/backups/ | head -1)
        rm -rf /root/fan-chain/data
        cp -r /root/fan-chain/backups/$LATEST_BACKUP /root/fan-chain/data

        # 重启节点
        cd /root/fan-chain && nohup ./fan-chain > output.log 2>&1 &

        echo "✅ 已回退到: $LATEST_BACKUP"

        # 发送告警（可选）
        # curl -X POST https://your-alert-webhook.com/alert \
        #   -d "Node2 auto-rollback triggered"
    fi

    sleep 30  # 每30秒检查一次
done
```

## 八、关键原则

1. **宁可停机，不可错账**
2. **先备份，后操作**
3. **有疑问就回退**
4. **总量不对立即停**

## 九、联系支持

如遇到无法解决的问题：
1. 保存错误日志
2. 记录问题发生时间
3. 备份错误状态数据
4. 查看 fan.md 最新更新

---

⚠️ **重要提醒**：P0 总量不变原则是绝对红线，任何情况下都不能妥协！