package network

import (
	"fan-chain/core"
	"fmt"
	"log"
	"net"
	"time"
)

// 接受连接循环
func (s *Server) acceptLoop() {
	for {
		select {
		case <-s.closeChan:
			return
		default:
		}

		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closeChan:
				return
			default:
				log.Printf("Accept error: %v", err)
				continue
			}
		}

		log.Printf("New inbound connection from %s", conn.RemoteAddr())
		peer := NewPeer(conn, false)
		s.addPeer(peer)
		peer.Start()

		// 发送Ping
		go s.sendPing(peer)
	}
}

// 连接到种子节点（支持DNS轮询，自动跳过自己）
func (s *Server) connectToSeeds() {
	for _, seed := range s.seedPeers {
		go func(host string) {
			// 解析host:port
			seedHost, seedPort, err := net.SplitHostPort(host)
			if err != nil {
				log.Printf("Invalid seed address %s: %v", host, err)
				return
			}

			// DNS解析获取所有IP
			ips, err := net.LookupIP(seedHost)
			if err != nil {
				// 如果解析失败，可能本身就是IP，直接尝试连接
				if err := s.ConnectToPeer(host); err != nil {
					log.Printf("Failed to connect to seed %s: %v", host, err)
				}
				return
			}

			// 尝试连接每个IP（跳过自己）
			connectedCount := 0
			for _, ip := range ips {
				ipStr := ip.String()
				peerAddr := net.JoinHostPort(ipStr, seedPort)

				// 跳过自己
				if s.isSelfAddress(ipStr) {
					log.Printf("Skipping self IP: %s", ipStr)
					continue
				}

				if err := s.ConnectToPeer(peerAddr); err != nil {
					log.Printf("Failed to connect to %s: %v", peerAddr, err)
				} else {
					connectedCount++
				}
			}
			log.Printf("Connected to %d/%d peers from %s", connectedCount, len(ips), host)
		}(seed)
	}
}

// 重连到种子节点（支持DNS轮询，自动跳过自己）
func (s *Server) reconnectToSeeds() {
	for _, seed := range s.seedPeers {
		go func(host string) {
			// 解析host:port
			seedHost, seedPort, err := net.SplitHostPort(host)
			if err != nil {
				return
			}

			// DNS解析获取所有IP
			ips, err := net.LookupIP(seedHost)
			if err != nil {
				// 如果解析失败，可能本身就是IP
				s.peersMu.RLock()
				_, connected := s.peers[host]
				s.peersMu.RUnlock()
				if !connected {
					if err := s.ConnectToPeer(host); err != nil {
						log.Printf("Failed to reconnect to seed %s: %v", host, err)
					}
				}
				return
			}

			// 尝试连接每个未连接的IP（跳过自己）
			for _, ip := range ips {
				ipStr := ip.String()
				peerAddr := net.JoinHostPort(ipStr, seedPort)

				// 跳过自己
				if s.isSelfAddress(ipStr) {
					continue
				}

				// 检查是否已连接
				s.peersMu.RLock()
				_, connected := s.peers[peerAddr]
				s.peersMu.RUnlock()

				if !connected {
					if err := s.ConnectToPeer(peerAddr); err != nil {
						log.Printf("Failed to reconnect to %s: %v", peerAddr, err)
					} else {
						log.Printf("Reconnected to %s", peerAddr)
					}
				}
			}
		}(seed)
	}
}

// 连接到对等节点
func (s *Server) ConnectToPeer(host string) error {
	// 检查是否已连接
	s.peersMu.RLock()
	if _, exists := s.peers[host]; exists {
		s.peersMu.RUnlock()
		return fmt.Errorf("already connected to %s", host)
	}
	s.peersMu.RUnlock()

	// 建立连接
	conn, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		return err
	}

	log.Printf("Connected to peer %s", host)
	peer := NewPeer(conn, true)
	s.addPeer(peer)
	peer.Start()

	// 发送Ping
	s.sendPing(peer)

	return nil
}

// 添加节点
func (s *Server) addPeer(peer *Peer) {
	s.peersMu.Lock()
	defer s.peersMu.Unlock()
	s.peers[peer.host] = peer
}

// 移除节点
func (s *Server) removePeer(host string) {
	s.peersMu.Lock()
	defer s.peersMu.Unlock()
	if peer, exists := s.peers[host]; exists {
		peer.Close()
		delete(s.peers, host)
	}
}

// 发送Ping（包含共识信息、区块哈希和checkpoint信息）
func (s *Server) sendPing(peer *Peer) {
	var height uint64
	var blockHash string
	if s.getLatestBlock != nil {
		latestBlock := s.getLatestBlock()
		if latestBlock != nil {
			height = latestBlock.Header.Height
			blockHash = latestBlock.Hash().String()
		}
	}

	// 获取最新checkpoint信息
	var checkpointHeight uint64
	var checkpointHash string
	var checkpointTimestamp int64
	if s.getLatestCheckpoint != nil {
		checkpoint, err := s.getLatestCheckpoint()
		if err == nil && checkpoint != nil {
			checkpointHeight = checkpoint.Height
			checkpointHash = checkpoint.BlockHash.String()
			checkpointTimestamp = checkpoint.Timestamp
		}
	}

	// 获取共识配置
	consensusConfig := core.GetConsensusConfig()

	ping := &PingMessage{
		Address:             s.address,
		Height:              height,
		LatestBlockHash:     blockHash,
		CheckpointHeight:    checkpointHeight,
		CheckpointHash:      checkpointHash,
		CheckpointTimestamp: checkpointTimestamp,
		ConsensusVersion:    consensusConfig.ConsensusVersion,
		ConsensusHash:       consensusConfig.ConsensusHash,
	}

	msg, err := NewMessage(MsgPing, ping)
	if err != nil {
		log.Printf("Failed to create ping message: %v", err)
		return
	}

	peer.SendMessage(msg)
}

// 获取在线节点列表（90秒内有心跳）
func (s *Server) GetOnlinePeers() []*Peer {
	s.peersMu.RLock()
	defer s.peersMu.RUnlock()

	online := make([]*Peer, 0)
	for _, peer := range s.peers {
		if peer.IsConnected() && peer.IsAlive() {
			online = append(online, peer)
		}
	}
	return online
}

// 判断是否是自己的IP地址
func (s *Server) isSelfAddress(ip string) bool {
	// 检查是否是本机地址
	if ip == "127.0.0.1" || ip == "localhost" || ip == "::1" {
		return true
	}

	// 【关键修复】检查配置的公网IP（用于NAT环境，如GCE云服务器）
	if s.publicIP != "" && ip == s.publicIP {
		return true
	}

	// 获取本机所有网卡IP
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if ipnet.IP.String() == ip {
				return true
			}
		}
	}

	return false
}

// 获取在线节点地址列表
func (s *Server) GetOnlinePeerAddresses() []string {
	peers := s.GetOnlinePeers()
	addresses := make([]string, 0, len(peers))
	for _, peer := range peers {
		addr := peer.GetAddress()
		if addr != "" {
			addresses = append(addresses, addr)
		}
	}
	return addresses
}
