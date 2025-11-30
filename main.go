package main

import (
	"flag"
	"log"
	"os"
	"time"

	"fan-chain/config"
)

func main() {
	configPath := flag.String("config", "", "Path to config file")
	flag.Parse()

	var cfg *config.Config
	var err error

	if *configPath != "" {
		cfg, err = config.LoadConfig(*configPath)
		if err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
	} else {
		if _, err := os.Stat("config.json"); err == nil {
			cfg, err = config.LoadConfig("config.json")
			if err != nil {
				log.Fatalf("Failed to load config.json: %v", err)
			}
		} else {
			cfg = config.DefaultConfig()
		}
	}

	node, err := NewNode(cfg)
	if err != nil {
		log.Fatalf("Failed to create node: %v", err)
	}
	defer node.Close()

	if err := node.LoadKeys(); err != nil {
		log.Fatalf("Failed to load keys: %v", err)
	}

	if err := node.InitializeBlockchain(); err != nil {
		log.Fatalf("Failed to initialize blockchain: %v", err)
	}

	if err := node.InitializeValidators(); err != nil {
		log.Fatalf("Failed to initialize validators: %v", err)
	}

	// 先启动P2P，准备接收checkpoint
	if err := node.InitializeP2P(); err != nil {
		log.Fatalf("Failed to initialize P2P: %v", err)
	}

	// 新节点使用checkpoint同步（唯一机制）
	// 这是FAN链的核心创新：新节点不从区块1同步，而是获取最新3个checkpoint+状态，快速入网
	// 如果checkpoint同步失败，输出错误日志供调试，但不中断启动（P2P会自动重试）
	if err := node.SyncFromCheckpoint(); err != nil {
		log.Printf("❌ Initial checkpoint sync failed: %v", err)
		log.Printf("⚠️  Node will continue startup, P2P will retry sync automatically")
	}

	// 【关键修复】等待checkpoint应用完成，然后判断验证者身份
	// Checkpoint接收和应用是异步的，需要等待
	log.Printf("⏳ Waiting for checkpoint sync to complete...")
	for i := 0; i < 10; i++ {
		time.Sleep(1 * time.Second)
		if node.chain.GetLatestHeight() > 0 {
			log.Printf("✓ Checkpoint applied, height: %d", node.chain.GetLatestHeight())
			break
		}
	}

	// 此时验证者集合已经从checkpoint恢复，可以正确判断
	isValidator := node.isActiveValidator(node.address)
	log.Printf("Node started: %s (Type: %s)", node.address, map[bool]string{true: "VALIDATOR", false: "FULL NODE"}[isValidator])

	// 如果节点需要checkpoint区块，启动完整的同步流程
	if node.needCheckpointBlock {
		log.Printf("🔄 Starting complete checkpoint+block sync with retry mechanism")
		go func() {
			if err := node.SyncCheckpointWithRetry(); err != nil {
				log.Printf("❌ Complete sync failed: %v", err)
			} else {
				log.Printf("✅ Complete sync successful")
			}
		}()
	}

	if err := node.InitializeAPI(); err != nil {
		log.Fatalf("Failed to initialize API: %v", err)
	}

	if len(cfg.SeedPeers) > 0 {
		time.Sleep(10 * time.Second)
	}

	node.StartCleanupTask()

	// 验证者激活机制（安全检查）
	if isValidator {
		log.Printf("Requesting validator activation...")
		if err := node.RequestValidatorActivation(); err != nil {
			log.Printf("⚠ Validator activation failed: %v", err)
			log.Printf("⚠ Validator will NOT produce blocks until activated")
			log.Printf("⚠ Starting activation monitor to retry after sync completes...")
			node.StartActivationMonitor()
		}
		node.StartBlockProduction()
	} else {
		select {}
	}
}
