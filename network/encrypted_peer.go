package network

import (
	"encoding/json"
	"fan-chain/crypto"
	"fmt"
	"log"
	"net"
)

// EncryptedPeer 加密对等节点
type EncryptedPeer struct {
	*Peer
	session       *crypto.EncryptedSession
	encryptionOn  bool
	localPrivKey  []byte
	localPubKey   []byte
	remotePubKey  []byte
}

// NewEncryptedPeer 创建加密对等节点
func NewEncryptedPeer(conn net.Conn, outbound bool, privateKey, publicKey []byte) *EncryptedPeer {
	return &EncryptedPeer{
		Peer:         NewPeer(conn, outbound),
		encryptionOn: false,
		localPrivKey: privateKey,
		localPubKey:  publicKey,
	}
}

// PerformHandshake 执行加密握手
func (ep *EncryptedPeer) PerformHandshake() error {
	if ep.outbound {
		// 作为客户端发起握手
		return ep.clientHandshake()
	} else {
		// 作为服务端响应握手
		return ep.serverHandshake()
	}
}

// clientHandshake 客户端握手流程
func (ep *EncryptedPeer) clientHandshake() error {
	// 1. 生成密钥交换请求
	req, err := crypto.GenerateKeyExchangeRequest(ep.localPrivKey, ep.localPubKey)
	if err != nil {
		return fmt.Errorf("failed to generate key exchange request: %v", err)
	}

	// 2. 发送请求
	reqData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %v", err)
	}

	msg := &Message{
		Type:    MsgKeyExchange,
		Payload: reqData,
	}

	if err := ep.Peer.SendMessage(msg); err != nil {
		return fmt.Errorf("failed to send key exchange request: %v", err)
	}

	// 3. 接收响应
	respMsg, err := ep.Peer.ReceiveMessage()
	if err != nil {
		return fmt.Errorf("failed to receive key exchange response: %v", err)
	}

	if respMsg.Type != MsgKeyExchange {
		return fmt.Errorf("unexpected message type: %d", respMsg.Type)
	}

	var resp crypto.KeyExchangeResponse
	if err := json.Unmarshal(respMsg.Payload, &resp); err != nil {
		return fmt.Errorf("failed to unmarshal response: %v", err)
	}

	// 4. 验证响应
	if !crypto.VerifyKeyExchangeResponse(&resp, req.Nonce) {
		return fmt.Errorf("key exchange response verification failed")
	}

	ep.remotePubKey = resp.PublicKey

	// 5. 派生共享密钥
	sharedSecret := crypto.DeriveSharedSecret(req.Nonce, resp.Nonce, ep.localPubKey, resp.PublicKey)
	sessionKey, err := crypto.DeriveSessionKey(sharedSecret)
	if err != nil {
		return fmt.Errorf("failed to derive session key: %v", err)
	}

	// 6. 创建加密会话
	session, err := crypto.NewEncryptedSession(sessionKey)
	if err != nil {
		return fmt.Errorf("failed to create encrypted session: %v", err)
	}

	ep.session = session
	ep.encryptionOn = true

	log.Printf("Client handshake completed with peer %s", ep.host)
	return nil
}

// serverHandshake 服务端握手流程
func (ep *EncryptedPeer) serverHandshake() error {
	// 1. 接收请求
	reqMsg, err := ep.Peer.ReceiveMessage()
	if err != nil {
		return fmt.Errorf("failed to receive key exchange request: %v", err)
	}

	if reqMsg.Type != MsgKeyExchange {
		return fmt.Errorf("unexpected message type: %d", reqMsg.Type)
	}

	var req crypto.KeyExchangeRequest
	if err := json.Unmarshal(reqMsg.Payload, &req); err != nil {
		return fmt.Errorf("failed to unmarshal request: %v", err)
	}

	// 2. 验证请求
	if !crypto.VerifyKeyExchangeRequest(&req) {
		return fmt.Errorf("key exchange request verification failed")
	}

	ep.remotePubKey = req.PublicKey

	// 3. 生成响应
	resp, err := crypto.GenerateKeyExchangeResponse(ep.localPrivKey, ep.localPubKey, req.Nonce)
	if err != nil {
		return fmt.Errorf("failed to generate key exchange response: %v", err)
	}

	// 4. 发送响应
	respData, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %v", err)
	}

	msg := &Message{
		Type:    MsgKeyExchange,
		Payload: respData,
	}

	if err := ep.Peer.SendMessage(msg); err != nil {
		return fmt.Errorf("failed to send key exchange response: %v", err)
	}

	// 5. 派生共享密钥
	sharedSecret := crypto.DeriveSharedSecret(resp.Nonce, req.Nonce, ep.localPubKey, req.PublicKey)
	sessionKey, err := crypto.DeriveSessionKey(sharedSecret)
	if err != nil {
		return fmt.Errorf("failed to derive session key: %v", err)
	}

	// 6. 创建加密会话
	session, err := crypto.NewEncryptedSession(sessionKey)
	if err != nil {
		return fmt.Errorf("failed to create encrypted session: %v", err)
	}

	ep.session = session
	ep.encryptionOn = true

	log.Printf("Server handshake completed with peer %s", ep.host)
	return nil
}

// SendEncryptedMessage 发送加密消息
func (ep *EncryptedPeer) SendEncryptedMessage(msg *Message) error {
	if !ep.encryptionOn {
		return fmt.Errorf("encryption not enabled")
	}

	// 序列化消息
	data, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	// 加密
	encrypted, err := ep.session.Encrypt(data)
	if err != nil {
		return fmt.Errorf("failed to encrypt message: %v", err)
	}

	// 发送加密消息
	encMsg := &Message{
		Type:    MsgEncrypted,
		Payload: encrypted,
	}

	return ep.Peer.SendMessage(encMsg)
}

// ReceiveEncryptedMessage 接收加密消息
func (ep *EncryptedPeer) ReceiveEncryptedMessage() (*Message, error) {
	if !ep.encryptionOn {
		return nil, fmt.Errorf("encryption not enabled")
	}

	// 接收加密消息
	encMsg, err := ep.Peer.ReceiveMessage()
	if err != nil {
		return nil, fmt.Errorf("failed to receive message: %v", err)
	}

	if encMsg.Type != MsgEncrypted {
		return nil, fmt.Errorf("unexpected message type: %d", encMsg.Type)
	}

	// 解密
	decrypted, err := ep.session.Decrypt(encMsg.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt message: %v", err)
	}

	// 反序列化
	msg, err := UnmarshalMessage(decrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal message: %v", err)
	}

	return msg, nil
}

// IsEncrypted 是否已加密
func (ep *EncryptedPeer) IsEncrypted() bool {
	return ep.encryptionOn
}

// GetRemotePublicKey 获取远程公钥
func (ep *EncryptedPeer) GetRemotePublicKey() []byte {
	return ep.remotePubKey
}
