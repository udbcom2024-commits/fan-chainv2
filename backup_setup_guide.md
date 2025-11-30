# Node2 备份配置指南

## 一、上传备份脚本

1. 将 `backup_data.sh` 上传到服务器：
```bash
scp backup_data.sh root@38.34.185.113:/root/fan-chain/
```

2. 给脚本添加执行权限：
```bash
chmod +x /root/fan-chain/backup_data.sh
```

## 二、在宝塔面板设置定时任务

1. 登录宝塔面板
2. 进入【计划任务】
3. 添加新任务：
   - 任务类型：Shell脚本
   - 任务名称：FAN_Node2_数据备份
   - 执行周期：每分钟（或选择"N分钟" 输入 1）
   - 脚本内容：
   ```bash
   /root/fan-chain/backup_data.sh
   ```

## 三、手动测试

在宝塔面板或SSH中执行：
```bash
/root/fan-chain/backup_data.sh
```

## 四、备份位置

- 备份保存路径：`/root/fan-chain/backups/`
- 备份格式：`backup_YYYYMMDD_HHMMSS`
- 自动保留最新 10 个备份

## 五、恢复备份

如需恢复某个备份：
```bash
# 停止节点
pkill -f fan-chain

# 备份当前数据（可选）
mv /root/fan-chain/data /root/fan-chain/data.old

# 恢复指定备份
cp -r /root/fan-chain/backups/backup_YYYYMMDD_HHMMSS /root/fan-chain/data

# 重启节点
cd /root/fan-chain && ./fan-chain
```

## 重要提醒

⚠️ **P0 总量不变原则**：每个备份都包含完整的状态数据，确保总量始终为 14 亿 FAN