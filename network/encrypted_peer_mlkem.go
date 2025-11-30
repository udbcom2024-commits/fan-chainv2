package network

import (
	"encoding/json"
	"fan-chain/crypto"
	"fmt"
	"log"
	"net"
)

// MLKEMEncryptedPeer 使用ML-KEM-768的加密对等节点
type MLKEMEncryptedPeer struct {
	*Peer
	session       *crypto.MLKEMEncryptedSession
	encryptionOn  bool
	localPrivKey  []byte // ML-DSA-65私钥
	localPubKey   []byte // ML-DSA-65公钥
	remotePubKey  []byte // 对方ML-DSA-65公钥
	kemPrivateKey []byte // 临时ML-KEM-768私钥（仅客户端保存）
}

// NewMLKEMEncryptedPeer 创建ML-KEM加密对等节点
func NewMLKEMEncryptedPeer(conn net.Conn, outbound bool, privateKey, publicKey []byte) *MLKEMEncryptedPeer {
	return &MLKEMEncryptedPeer{
		Peer:         NewPeer(conn, outbound),
		encryptionOn: false,
		localPrivKey: privateKey,
		localPubKey:  publicKey,
	}
}

// PerformMLKEMHandshake 执行ML-KEM加密握手
func (ep *MLKEMEncryptedPeer) PerformMLKEMHandshake() error {
	if ep.outbound {
		// 作为客户端发起握手
		return ep.mlkemClientHandshake()
	} else {
		// 作为服务端响应握手
		return ep.mlkemServerHandshake()
	}
}

// mlkemClientHandshake 客户端ML-KEM握手流程
func (ep *MLKEMEncryptedPeer) mlkemClientHandshake() error {
	// 1. 生成ML-KEM密钥交换请求
	req, kemPrivKey, err := crypto.GenerateMLKEMKeyExchangeRequest(ep.localPrivKey, ep.localPubKey)
	if err != nil {
		return fmt.Errorf("failed to generate ML-KEM key exchange request: %v", err)
	}

	// 保存KEM私钥，稍后用于解封装
	ep.kemPrivateKey = kemPrivKey

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
		return fmt.Errorf("failed to send ML-KEM key exchange request: %v", err)
	}

	log.Printf("Client: Sent ML-KEM key exchange request to %s", ep.host)

	// 3. 接收响应
	respMsg, err := ep.Peer.ReceiveMessage()
	if err != nil {
		return fmt.Errorf("failed to receive ML-KEM key exchange response: %v", err)
	}

	if respMsg.Type != MsgKeyExchange {
		return fmt.Errorf("unexpected message type: %d", respMsg.Type)
	}

	var resp crypto.MLKEMKeyExchangeResponse
	if err := json.Unmarshal(respMsg.Payload, &resp); err != nil {
		return fmt.Errorf("failed to unmarshal response: %v", err)
	}

	// 4. 验证响应签名
	if !crypto.VerifyMLKEMKeyExchangeResponse(&resp) {
		return fmt.Errorf("ML-KEM key exchange response verification failed")
	}

	ep.remotePubKey = resp.SignaturePublicKey

	// 5. 解封装共享密钥
	sharedSecret, err := crypto.DecapsulateSharedSecret(ep.kemPrivateKey, resp.Ciphertext)
	if err != nil {
		return fmt.Errorf("failed to decapsulate shared secret: %v", err)
	}

	// 6. 创建加密会话
	session, err := crypto.NewMLKEMEncryptedSession(sharedSecret)
	if err != nil {
		return fmt.Errorf("failed to create encrypted session: %v", err)
	}

	ep.session = session
	ep.encryptionOn = true

	// 清除临时KEM私钥（前向保密）
	for i := range ep.kemPrivateKey {
		ep.kemPrivateKey[i] = 0
	}
	ep.kemPrivateKey = nil

	log.Printf("Client: ML-KEM handshake completed with peer %s (quantum-safe)", ep.host)
	return nil
}

// mlkemServerHandshake 服务端ML-KEM握手流程
func (ep *MLKEMEncryptedPeer) mlkemServerHandshake() error {
	// 1. 接收请求
	reqMsg, err := ep.Peer.ReceiveMessage()
	if err != nil {
		return fmt.Errorf("failed to receive ML-KEM key exchange request: %v", err)
	}

	if reqMsg.Type != MsgKeyExchange {
		return fmt.Errorf("unexpected message type: %d", reqMsg.Type)
	}

	var req crypto.MLKEMKeyExchangeRequest
	if err := json.Unmarshal(reqMsg.Payload, &req); err != nil {
		return fmt.Errorf("failed to unmarshal request: %v", err)
	}

	// 2. 验证请求签名
	if !crypto.VerifyMLKEMKeyExchangeRequest(&req) {
		return fmt.Errorf("ML-KEM key exchange request verification failed")
	}

	ep.remotePubKey = req.SignaturePublicKey

	log.Printf("Server: Received ML-KEM key exchange request from %s", ep.host)

	// 3. 生成响应（封装共享密钥）
	resp, sharedSecret, err := crypto.GenerateMLKEMKeyExchangeResponse(
		ep.localPrivKey,
		ep.localPubKey,
		req.KEMPublicKey,
	)
	if err != nil {
		return fmt.Errorf("failed to generate ML-KEM key exchange response: %v", err)
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
		return fmt.Errorf("failed to send ML-KEM key exchange response: %v", err)
	}

	log.Printf("Server: Sent ML-KEM key exchange response to %s", ep.host)

	// 5. 创建加密会话
	session, err := crypto.NewMLKEMEncryptedSession(sharedSecret)
	if err != nil {
		return fmt.Errorf("failed to create encrypted session: %v", err)
	}

	ep.session = session
	ep.encryptionOn = true

	log.Printf("Server: ML-KEM handshake completed with peer %s (quantum-safe)", ep.host)
	return nil
}

// SendEncryptedMessage 发送加密消息
func (ep *MLKEMEncryptedPeer) SendEncryptedMessage(msg *Message) error {
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
func (ep *MLKEMEncryptedPeer) ReceiveEncryptedMessage() (*Message, error) {
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
func (ep *MLKEMEncryptedPeer) IsEncrypted() bool {
	return ep.encryptionOn
}

// GetRemotePublicKey 获取远程公钥
func (ep *MLKEMEncryptedPeer) GetRemotePublicKey() []byte {
	return ep.remotePubKey
}
