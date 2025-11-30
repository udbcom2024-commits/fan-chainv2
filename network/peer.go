package network

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// 对等节点
type Peer struct {
	conn      net.Conn
	address   string // 节点地址
	host      string // IP:Port
	reader    *bufio.Reader
	writer    *bufio.Writer
	outbound  bool // 是否是出站连接
	connected bool
	mu        sync.Mutex

	// 消息通道
	sendChan chan *Message
	recvChan chan *Message
	closeChan chan struct{}

	// 心跳检测
	lastHeartbeat time.Time
	heartbeatMu   sync.RWMutex

	// 【家长制】peer高度跟踪（用于Failover决策）
	height   uint64    // peer报告的高度
	heightMu sync.RWMutex
}

// 创建对等节点
func NewPeer(conn net.Conn, outbound bool) *Peer {
	return &Peer{
		conn:          conn,
		host:          conn.RemoteAddr().String(),
		reader:        bufio.NewReader(conn),
		writer:        bufio.NewWriter(conn),
		outbound:      outbound,
		connected:     true,
		sendChan:      make(chan *Message, 100),
		recvChan:      make(chan *Message, 100),
		closeChan:     make(chan struct{}),
		lastHeartbeat: time.Now(),
	}
}

// 启动对等节点
func (p *Peer) Start() {
	go p.readLoop()
	go p.writeLoop()
}

// 读取循环
func (p *Peer) readLoop() {
	defer p.Close()

	for {
		select {
		case <-p.closeChan:
			return
		default:
		}

		// 设置读取超时
		p.conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		// 读取消息长度（4字节）
		var length uint32
		if err := binary.Read(p.reader, binary.BigEndian, &length); err != nil {
			if err != io.EOF {
				log.Printf("Peer %s read length error: %v", p.host, err)
			}
			return
		}

		// 限制消息大小（最大10MB）
		if length > 10*1024*1024 {
			log.Printf("Peer %s message too large: %d bytes", p.host, length)
			return
		}

		// 读取消息内容
		data := make([]byte, length)
		if _, err := io.ReadFull(p.reader, data); err != nil {
			log.Printf("Peer %s read data error: %v", p.host, err)
			return
		}

		// 反序列化消息
		msg, err := UnmarshalMessage(data)
		if err != nil {
			log.Printf("Peer %s unmarshal error: %v", p.host, err)
			continue
		}

		// 发送到接收通道
		select {
		case p.recvChan <- msg:
		case <-p.closeChan:
			return
		default:
			log.Printf("Peer %s receive channel full, dropping message", p.host)
		}
	}
}

// 写入循环
func (p *Peer) writeLoop() {
	defer p.Close()

	for {
		select {
		case <-p.closeChan:
			return
		case msg := <-p.sendChan:
			if err := p.writeMessage(msg); err != nil {
				log.Printf("Peer %s write error: %v", p.host, err)
				return
			}
		}
	}
}

// 写入消息
func (p *Peer) writeMessage(msg *Message) error {
	// 序列化消息
	data, err := msg.Marshal()
	if err != nil {
		return err
	}

	// 写入消息长度
	length := uint32(len(data))
	if err := binary.Write(p.writer, binary.BigEndian, length); err != nil {
		return err
	}

	// 写入消息内容
	if _, err := p.writer.Write(data); err != nil {
		return err
	}

	// 刷新缓冲区
	return p.writer.Flush()
}

// 发送消息
func (p *Peer) SendMessage(msg *Message) error {
	p.mu.Lock()
	if !p.connected {
		p.mu.Unlock()
		return fmt.Errorf("peer not connected")
	}
	p.mu.Unlock()

	select {
	case p.sendChan <- msg:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("send timeout")
	}
}

// 接收消息
func (p *Peer) ReceiveMessage() (*Message, error) {
	select {
	case msg := <-p.recvChan:
		return msg, nil
	case <-p.closeChan:
		return nil, fmt.Errorf("peer closed")
	}
}

// 设置节点地址
func (p *Peer) SetAddress(address string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.address = address
}

// 获取节点地址
func (p *Peer) GetAddress() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.address
}

// 更新心跳时间
func (p *Peer) UpdateHeartbeat() {
	p.heartbeatMu.Lock()
	defer p.heartbeatMu.Unlock()
	p.lastHeartbeat = time.Now()
}

// 获取上次心跳时间
func (p *Peer) GetLastHeartbeat() time.Time {
	p.heartbeatMu.RLock()
	defer p.heartbeatMu.RUnlock()
	return p.lastHeartbeat
}

// 检查节点是否在线（90秒内有心跳）
func (p *Peer) IsAlive() bool {
	p.heartbeatMu.RLock()
	defer p.heartbeatMu.RUnlock()
	return time.Since(p.lastHeartbeat) < 90*time.Second
}

// 关闭连接
func (p *Peer) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.connected {
		return
	}

	p.connected = false
	close(p.closeChan)
	p.conn.Close()
	log.Printf("Peer %s closed", p.host)
}

// 是否已连接
func (p *Peer) IsConnected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connected
}

// 【家长制】更新peer高度（收到Pong时调用）
func (p *Peer) SetHeight(height uint64) {
	p.heightMu.Lock()
	defer p.heightMu.Unlock()
	p.height = height
}

// 【家长制】获取peer高度
func (p *Peer) GetHeight() uint64 {
	p.heightMu.RLock()
	defer p.heightMu.RUnlock()
	return p.height
}
