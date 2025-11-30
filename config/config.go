package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// 配置
type Config struct {
	DataDir string `json:"data_dir"`

	// 节点信息
	NodeName       string `json:"node_name"`        // 节点名称（仅显示用，不参与共识）
	NodeAddress    string `json:"node_address"`
	PrivateKeyFile string `json:"private_key_file"`
	PublicKeyFile  string `json:"public_key_file"`

	// 网络
	P2PPort   int      `json:"p2p_port"`
	APIPort   int      `json:"api_port"`
	P2PHost   string   `json:"p2p_host"`
	PublicIP  string   `json:"public_ip"`  // 公网IP（用于NAT环境下跳过自己）
	SeedPeers []string `json:"seed_peers"`

	// 注意：Checkpoint配置已移至consensus.json（共识参数）
	// CheckpointInterval 和 CheckpointKeepCount 现在从 core.GetConsensusConfig() 获取
}

// 默认配置
func DefaultConfig() *Config {
	return &Config{
		DataDir:        "./data",
		NodeName:       "",
		NodeAddress:    "",
		PrivateKeyFile: "",
		PublicKeyFile:  "",
		P2PPort:        9001,
		APIPort:        9000,
		P2PHost:        "0.0.0.0",
		SeedPeers:      []string{},
	}
}

// 从环境变量应用覆盖
func (cfg *Config) ApplyEnv() {
	if v := os.Getenv("FAN_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("FAN_NODE_ADDRESS"); v != "" {
		cfg.NodeAddress = v
	}
	if v := os.Getenv("FAN_PRIVATE_KEY_FILE"); v != "" {
		cfg.PrivateKeyFile = v
	}
	if v := os.Getenv("FAN_PUBLIC_KEY_FILE"); v != "" {
		cfg.PublicKeyFile = v
	}
	if v := os.Getenv("FAN_P2P_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.P2PPort = port
		}
	}
	if v := os.Getenv("FAN_API_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.APIPort = port
		}
	}
	if v := os.Getenv("FAN_P2P_HOST"); v != "" {
		cfg.P2PHost = v
	}
	if v := os.Getenv("FAN_SEED_PEERS"); v != "" {
		cfg.SeedPeers = strings.Split(v, ",")
		for i := range cfg.SeedPeers {
			cfg.SeedPeers[i] = strings.TrimSpace(cfg.SeedPeers[i])
		}
	}
}

// 从文件加载配置
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %v", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %v", err)
	}

	cfg.ApplyEnv()

	return &cfg, nil
}

// 保存配置
func (cfg *Config) Save(path string) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// 确保目录存在
func (cfg *Config) EnsureDirs() error {
	return os.MkdirAll(cfg.DataDir, 0700)
}

// 获取数据库路径
func (cfg *Config) DBPath() string {
	return filepath.Join(cfg.DataDir, "blockchain.db")
}
