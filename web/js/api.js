// FAN链API交互模块

const API_BASE = '';

// API工具函数
const API = {
    // 获取节点状态
    async getStatus() {
        const response = await fetch(`${API_BASE}/status`);
        if (!response.ok) throw new Error('获取状态失败');
        return await response.json();
    },

    // 获取区块列表
    async getBlocks(page = 1, limit = 20) {
        const response = await fetch(`${API_BASE}/blocks?page=${page}&limit=${limit}`);
        if (!response.ok) throw new Error('获取区块列表失败');
        return await response.json();
    },

    // 获取单个区块
    async getBlock(height) {
        const response = await fetch(`${API_BASE}/block/${height}`);
        if (!response.ok) throw new Error('获取区块失败');
        return await response.json();
    },

    // 获取交易列表
    async getTransactions(page = 1, limit = 20) {
        const response = await fetch(`${API_BASE}/transactions?page=${page}&limit=${limit}`);
        if (!response.ok) throw new Error('获取交易列表失败');
        return await response.json();
    },

    // 获取单个交易
    async getTransaction(hash) {
        const response = await fetch(`${API_BASE}/transaction/${hash}`);
        if (!response.ok) throw new Error('获取交易失败');
        return await response.json();
    },

    // 获取账户余额
    async getBalance(address) {
        const response = await fetch(`${API_BASE}/balance/${address}`);
        if (!response.ok) throw new Error('获取账户余额失败');
        return await response.json();
    },

    // 获取账户交易历史
    async getAccountTransactions(address, page = 1, limit = 20) {
        const response = await fetch(`${API_BASE}/account/${address}/transactions?page=${page}&limit=${limit}`);
        if (!response.ok) throw new Error('获取账户交易失败');
        return await response.json();
    },

    // 获取转账列表（仅Type=1的普通转账）
    async getTransfers(page = 1, limit = 20) {
        const response = await fetch(`${API_BASE}/transfers?page=${page}&limit=${limit}`);
        if (!response.ok) throw new Error('获取转账列表失败');
        return await response.json();
    },

    // 获取账户列表（按余额排序）
    async getAccounts(page = 1, limit = 20) {
        const response = await fetch(`${API_BASE}/accounts?page=${page}&limit=${limit}`);
        if (!response.ok) throw new Error('获取账户列表失败');
        return await response.json();
    },

    // 搜索（区块高度、交易哈希、地址）
    async search(query) {
        const response = await fetch(`${API_BASE}/search?q=${encodeURIComponent(query)}`);
        if (!response.ok) throw new Error('搜索失败');
        return await response.json();
    }
};

// 工具函数
const Utils = {
    // 格式化FAN数量（从最小单位转换为FAN）
    formatAmount(amount) {
        const fan = amount / 1000000;
        // 不使用千分位逗号，保持纯数字
        return fan.toString() + ' FAN';
    },

    // 格式化时间戳
    formatTimestamp(timestamp) {
        const date = new Date(timestamp * 1000);
        const now = new Date();
        const diff = Math.floor((now - date) / 1000);

        // 处理时间戳比当前时间新的情况（服务器时间可能稍快）
        // 如果差异在60秒内（包括未来30秒），统一显示为"刚刚"
        if (Math.abs(diff) <= 60) return '刚刚';

        if (diff < 60) return `${diff}秒前`;
        if (diff < 3600) return `${Math.floor(diff / 60)}分钟前`;
        if (diff < 86400) return `${Math.floor(diff / 3600)}小时前`;
        if (diff < 2592000) return `${Math.floor(diff / 86400)}天前`;

        return date.toLocaleString('zh-CN', {
            year: 'numeric',
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit'
        });
    },

    // 格式化完整时间
    formatFullTime(timestamp) {
        const date = new Date(timestamp * 1000);
        return date.toLocaleString('zh-CN', {
            year: 'numeric',
            month: '2-digit',
            day: '2-digit',
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit'
        });
    },

    // 截断哈希/地址
    truncateHash(hash, startLen = 10, endLen = 8) {
        if (!hash || hash.length <= startLen + endLen) return hash;
        return `${hash.substring(0, startLen)}...${hash.substring(hash.length - endLen)}`;
    },

    // 完整显示哈希/地址
    fullHash(hash) {
        return hash;
    },

    // 获取交易类型文本
    getTxTypeText(type) {
        const types = {
            1: '转账',
            2: '质押',
            3: '奖励',
            4: '解押',
            5: '惩罚'
        };
        return types[type] || '未知';
    },

    // 获取交易类型徽章类别
    getTxTypeBadge(type) {
        const badges = {
            1: 'info',
            2: 'success',
            3: 'success',
            4: 'warning',
            5: 'danger'
        };
        return badges[type] || 'info';
    },

    // 复制到剪贴板
    async copyToClipboard(text) {
        try {
            await navigator.clipboard.writeText(text);
            showNotification('已复制到剪贴板', 'success');
        } catch (err) {
            console.error('复制失败:', err);
            showNotification('复制失败', 'danger');
        }
    }
};

// 显示通知
function showNotification(message, type = 'info') {
    const notification = document.createElement('div');
    notification.className = `notification notification-${type}`;
    notification.textContent = message;
    notification.style.cssText = `
        position: fixed;
        top: 80px;
        right: 20px;
        background: ${type === 'success' ? '#2ecc71' : type === 'danger' ? '#e74c3c' : '#3498db'};
        color: white;
        padding: 16px 24px;
        border-radius: 8px;
        box-shadow: 0 4px 12px rgba(0,0,0,0.3);
        z-index: 9999;
        animation: slideIn 0.3s ease-out;
    `;

    document.body.appendChild(notification);

    setTimeout(() => {
        notification.style.animation = 'slideOut 0.3s ease-out';
        setTimeout(() => notification.remove(), 300);
    }, 3000);
}

// 添加动画样式
const style = document.createElement('style');
style.textContent = `
    @keyframes slideIn {
        from {
            transform: translateX(400px);
            opacity: 0;
        }
        to {
            transform: translateX(0);
            opacity: 1;
        }
    }
    @keyframes slideOut {
        from {
            transform: translateX(0);
            opacity: 1;
        }
        to {
            transform: translateX(400px);
            opacity: 0;
        }
    }
`;
document.head.appendChild(style);

// WebSocket连接（用于实时更新）
class WebSocketClient {
    constructor() {
        this.ws = null;
        this.listeners = new Map();
        this.reconnectDelay = 3000;
        this.maxReconnectDelay = 30000;
        this.currentDelay = this.reconnectDelay;
    }

    connect() {
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/ws`;

        this.ws = new WebSocket(wsUrl);

        this.ws.onopen = () => {
            console.log('WebSocket已连接');
            this.currentDelay = this.reconnectDelay;
        };

        this.ws.onmessage = (event) => {
            try {
                const data = JSON.parse(event.data);
                this.emit(data.type, data.payload);
            } catch (err) {
                console.error('WebSocket消息解析失败:', err);
            }
        };

        this.ws.onerror = (error) => {
            console.error('WebSocket错误:', error);
        };

        this.ws.onclose = () => {
            console.log('WebSocket已断开，尝试重连...');
            setTimeout(() => {
                this.currentDelay = Math.min(this.currentDelay * 1.5, this.maxReconnectDelay);
                this.connect();
            }, this.currentDelay);
        };
    }

    on(event, callback) {
        if (!this.listeners.has(event)) {
            this.listeners.set(event, []);
        }
        this.listeners.get(event).push(callback);
    }

    emit(event, data) {
        if (this.listeners.has(event)) {
            this.listeners.get(event).forEach(callback => callback(data));
        }
    }

    disconnect() {
        if (this.ws) {
            this.ws.close();
            this.ws = null;
        }
    }
}

// 导出全局对象
window.API = API;
window.Utils = Utils;
window.WebSocketClient = WebSocketClient;
