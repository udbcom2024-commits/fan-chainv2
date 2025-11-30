package network

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"fan-chain/core"
)

// P2P服务器
type Server struct {
	address  string // 本节点地址
	port     int    // 监听端口
	publicIP string // 本节点公网IP（用于跳过自己）
	listener net.Listener

	peers   map[string]*Peer // 连接的节点 (host -> peer)
	peersMu sync.RWMutex

	// 种子节点
	seedPeers []string

	// 区块链接口
	blockchain          *core.Blockchain
	getLatestBlock      func() *core.Block
	addBlock            func(*core.Block) error
	addBlockSkipTimestamp func(*core.Block) error                                  // 添加区块（跳过时间戳检查，用于同步历史区块）
	getBlockRange       func(uint64, uint64) ([]*core.Block, error)
	verifyProposer      func(height uint64, prevBlock *core.Block) (string, error) // VRF验证proposer
	performReorg        func(rollbackHeight uint64, correctBlock *core.Block) error // 执行链重组
	getProposerStake    func(address string) uint64                                 // 获取proposer的质押
	getLatestCheckpoints     func(count int) []CheckpointInfo                            // 获取最新N个checkpoint
	getLatestCheckpoint      func() (*core.Checkpoint, error)                            // 获取最新checkpoint
	applyCheckpoint          func(*core.Checkpoint) error                                // 应用checkpoint
	getStateSnapshot         func(uint64) ([]byte, error)                                // 获取状态快照
	applyStateSnapshot       func(uint64, []byte) error                                  // 应用状态快照
	handleReceivedTransaction func(*core.Transaction) error                              // 处理接收到的交易
	detectAndResolveFork     func(peerHeight uint64, peerBlockHash string, peerCheckpointHeight uint64, peerCheckpointHash string, peerCheckpointTimestamp int64) error // 检测并解决分叉（谁快认谁做大哥）

	// 控制通道
	closeChan chan struct{}
	running   bool
	mu        sync.Mutex

	// 同步状态
	syncing            bool
	syncMu             sync.Mutex
	syncTargetHeight   uint64    // 目标同步高度
	syncPeer           *Peer     // 同步来源节点
	syncStartTime      time.Time // 同步开始时间
	lastSyncHeight     uint64    // 上次同步到的高度
	progressiveSkip    uint64    // 渐进式跳过数量(0→1000→10000→100000)
	checkpointSyncFrom uint64    // checkpoint同步起点（checkpoint高度-一个周期）
	checkpointHeight   uint64    // checkpoint高度（用于判断是否需要回填历史区块）
	saveBlockOnly      func(*core.Block) error // 只保存区块（用于回填历史）
	replaceForkedBlock func(*core.Block) error // 【家规】替换分叉区块（强制用大哥的覆盖本地的）

	// 【P2协议】向下同步（backfill）状态
	backfillInProgress   bool      // 是否正在向下同步
	backfillTargetHeight uint64    // 向下同步目标高度（大哥的最早区块）
	backfillCurrentFrom  uint64    // 当前向下同步的起始高度
	getEarliestHeight    func() uint64 // 获取本节点最早区块高度

	// 区块广播重试机制
	lastBroadcastHeight uint64    // 上次广播的区块高度
	lastBroadcastTime   time.Time // 上次广播时间
	broadcastMu         sync.Mutex

	// 【简单多数认输机制】统计被拒绝的proposer
	// key: height, value: map[proposer]count
	rejectedProposers   map[uint64]map[string]int
	rejectedProposersMu sync.Mutex

	// 【家长制优化】验证者判断回调（只有验证者的高度才影响出块决策）
	// 解决：非验证者节点（如History节点）的高度不应阻塞验证者出块
	isValidator func(address string) bool
}

// 创建P2P服务器
func NewServer(address string, port int, seedPeers []string, publicIP string) *Server {
	return &Server{
		address:           address,
		port:              port,
		publicIP:          publicIP,
		seedPeers:         seedPeers,
		peers:             make(map[string]*Peer),
		closeChan:         make(chan struct{}),
		rejectedProposers: make(map[uint64]map[string]int), // 初始化简单多数计数器
	}
}

// 设置区块链接口
func (s *Server) SetBlockchainInterface(
	getLatestBlock func() *core.Block,
	addBlock func(*core.Block) error,
	getBlockRange func(uint64, uint64) ([]*core.Block, error),
) {
	s.getLatestBlock = getLatestBlock
	s.addBlock = addBlock
	s.getBlockRange = getBlockRange
}

// 设置跳过时间戳检查的区块添加函数（用于同步历史区块）
func (s *Server) SetAddBlockSkipTimestamp(fn func(*core.Block) error) {
	s.addBlockSkipTimestamp = fn
}

// 设置只保存区块的函数（用于checkpoint前回填历史区块）
func (s *Server) SetSaveBlockOnly(fn func(*core.Block) error) {
	s.saveBlockOnly = fn
}

// 设置proposer验证函数（用于防止分叉）
func (s *Server) SetVerifyProposer(fn func(height uint64, prevBlock *core.Block) (string, error)) {
	s.verifyProposer = fn
}

// 设置链重组函数（用于自动修复分叉）
func (s *Server) SetPerformReorg(fn func(rollbackHeight uint64, correctBlock *core.Block) error) {
	s.performReorg = fn
}

// 设置获取proposer质押的函数（用于质押权重链选择）
func (s *Server) SetGetProposerStake(fn func(address string) uint64) {
	s.getProposerStake = fn
}

// 设置获取最新N个checkpoint的函数
func (s *Server) SetGetLatestCheckpoints(fn func(count int) []CheckpointInfo) {
	s.getLatestCheckpoints = fn
}

// 设置获取最新checkpoint的函数
func (s *Server) SetGetLatestCheckpoint(fn func() (*core.Checkpoint, error)) {
	s.getLatestCheckpoint = fn
}

// 设置应用checkpoint的函数
func (s *Server) SetApplyCheckpoint(fn func(*core.Checkpoint) error) {
	s.applyCheckpoint = fn
}

// 设置分叉检测和解决函数（谁快认谁做大哥）
func (s *Server) SetDetectAndResolveFork(fn func(peerHeight uint64, peerBlockHash string, peerCheckpointHeight uint64, peerCheckpointHash string, peerCheckpointTimestamp int64) error) {
	s.detectAndResolveFork = fn
}

// 设置获取状态快照的函数
func (s *Server) SetGetStateSnapshot(fn func(uint64) ([]byte, error)) {
	s.getStateSnapshot = fn
}

// 设置应用状态快照的函数
func (s *Server) SetApplyStateSnapshot(fn func(uint64, []byte) error) {
	s.applyStateSnapshot = fn
}

// 【家规】设置替换分叉区块的函数（强制用大哥的覆盖本地的）
func (s *Server) SetReplaceForkedBlock(fn func(*core.Block) error) {
	s.replaceForkedBlock = fn
}

// 【P2协议】设置获取本节点最早区块高度的函数
func (s *Server) SetGetEarliestHeight(fn func() uint64) {
	s.getEarliestHeight = fn
}

// 【家长制优化】设置验证者判断回调
func (s *Server) SetIsValidator(fn func(address string) bool) {
	s.isValidator = fn
}

// 【P2协议】检查向下同步是否完成
func (s *Server) IsBackfillComplete() bool {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return !s.backfillInProgress
}

// 【P2协议】获取向下同步目标高度
func (s *Server) GetBackfillTargetHeight() uint64 {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return s.backfillTargetHeight
}

// 启动服务器
func (s *Server) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}
	s.running = true
	s.mu.Unlock()

	// 监听端口
	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}
	s.listener = listener

	log.Printf("P2P server listening on port %d", s.port)

	// 启动接受连接循环
	go s.acceptLoop()

	// 连接到种子节点
	go s.connectToSeeds()

	// 启动消息处理循环
	go s.messageLoop()

	// 启动种子节点重连循环（每30秒检查一次）
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.reconnectToSeeds()
			case <-s.closeChan:
				return
			}
		}
	}()

	// 启动心跳循环（每30秒发送一次心跳）
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.sendHeartbeatToAll()
			case <-s.closeChan:
				return
			}
		}
	}()

	// 启动同步超时监控循环（每5秒检查一次）
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.checkSyncTimeout()
			case <-s.closeChan:
				return
			}
		}
	}()

	// 启动广播重试监控循环（每2秒检查一次，若高度停滞6秒则重新广播）
	go s.monitorAndRetryBroadcast()

	return nil
}

// 停止服务器
func (s *Server) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	close(s.closeChan)

	if s.listener != nil {
		s.listener.Close()
	}

	// 关闭所有节点连接
	s.peersMu.Lock()
	for _, peer := range s.peers {
		peer.Close()
	}
	s.peers = make(map[string]*Peer)
	s.peersMu.Unlock()

	log.Println("P2P server stopped")
}

// 获取连接的节点数
func (s *Server) PeerCount() int {
	s.peersMu.RLock()
	defer s.peersMu.RUnlock()
	return len(s.peers)
}

// 【家长制】获取所有验证者peer中最高的高度（用于Failover决策）
// 如果有"大哥"（验证者peer高度更高），不应该Failover出块
// 注意：只统计验证者节点，非验证者（如History节点）的高度不影响出块决策
func (s *Server) GetBestPeerHeight() uint64 {
	s.peersMu.RLock()
	defer s.peersMu.RUnlock()

	var bestHeight uint64
	for _, peer := range s.peers {
		if peer.IsConnected() {
			// 【家长制优化】只统计验证者peer的高度
			peerAddr := peer.GetAddress()
			if s.isValidator != nil && peerAddr != "" {
				if !s.isValidator(peerAddr) {
					// 非验证者，跳过
					continue
				}
			}
			peerHeight := peer.GetHeight()
			if peerHeight > bestHeight {
				bestHeight = peerHeight
			}
		}
	}
	return bestHeight
}

// RequestCheckpointFromPeers 主动从peers请求最新的N个checkpoint
func (s *Server) RequestCheckpointFromPeers(count uint64) {
	s.peersMu.RLock()
	defer s.peersMu.RUnlock()

	for _, peer := range s.peers {
		if peer.IsConnected() {
			log.Printf("Requesting %d checkpoints from peer %s", count, peer.host)
			msg, err := NewMessage(MsgGetCheckpoint, &GetCheckpointMessage{Count: count})
			if err != nil {
				log.Printf("Failed to create checkpoint request: %v", err)
				continue
			}
			peer.SendMessage(msg)
			// 只向第一个可用peer请求
			break
		}
	}
}

// 设置交易处理回调
func (s *Server) SetHandleReceivedTransaction(fn func(*core.Transaction) error) {
	s.handleReceivedTransaction = fn
}

// 广播交易到所有节点
func (s *Server) BroadcastTransaction(tx *core.Transaction) {
	s.peersMu.RLock()
	defer s.peersMu.RUnlock()

	if len(s.peers) == 0 {
		log.Printf("No peers to broadcast transaction")
		return
	}

	msg, err := NewMessage(MsgTransaction, &TransactionMessage{Transaction: tx})
	if err != nil {
		log.Printf("Failed to create transaction message: %v", err)
		return
	}

	broadcastCount := 0
	for _, peer := range s.peers {
		if peer.IsConnected() {
			peer.SendMessage(msg)
			broadcastCount++
		}
	}

	log.Printf("Broadcasted transaction %x to %d peers", tx.Hash().Bytes()[:8], broadcastCount)
}

// 【简单多数认输机制】记录拒绝的proposer，返回是否应该认输
// height: 区块高度
// proposer: 被拒绝区块的proposer地址
// 返回: (shouldSurrender bool, winningProposer string)
func (s *Server) RecordRejectedProposer(height uint64, proposer string) (bool, string) {
	s.rejectedProposersMu.Lock()
	defer s.rejectedProposersMu.Unlock()

	// 初始化该高度的map
	if s.rejectedProposers[height] == nil {
		s.rejectedProposers[height] = make(map[string]int)
	}

	// 增加计数
	s.rejectedProposers[height][proposer]++
	count := s.rejectedProposers[height][proposer]

	// 获取peer总数
	s.peersMu.RLock()
	peerCount := len(s.peers)
	s.peersMu.RUnlock()

	// 简单多数 = peer数/2 + 1 (至少2)
	majority := peerCount/2 + 1
	if majority < 2 {
		majority = 2
	}

	log.Printf("📊 【简单多数】Height %d: %s got %d/%d votes (majority: %d)",
		height, proposer[:10], count, peerCount, majority)

	// 如果同一个proposer被超过半数peer发送，说明我们的VRF计算错了
	if count >= majority {
		log.Printf("⚠️ 【简单多数认输】Height %d: %d peers agree on %s, my VRF must be wrong!",
			height, count, proposer[:10])
		return true, proposer
	}

	return false, ""
}

// 清理旧的拒绝记录（防止内存泄漏）
func (s *Server) CleanupRejectedProposers(currentHeight uint64) {
	s.rejectedProposersMu.Lock()
	defer s.rejectedProposersMu.Unlock()

	// 只保留最近100个高度的记录
	for h := range s.rejectedProposers {
		if h+100 < currentHeight {
			delete(s.rejectedProposers, h)
		}
	}
}
