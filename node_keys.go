package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fan-chain/core"
)

const keysSetupHelp = `
================================================================================
                         FAN Chain Node Key Configuration
================================================================================

ERROR: Node keys not configured. This node cannot start without valid keys.

Please follow these steps to configure your node:

1. Generate a new keypair using the keygen tool:
   cd tools && go run keygen.go generate -o ../keys -n node

2. This will create three files in the keys/ directory:
   - node_private.key  (keep this secret!)
   - node_public.key
   - node_address.txt

3. Create keys/node_address.json with your address:
   {
     "address": "YOUR_ADDRESS_FROM_node_address.txt"
   }

4. Verify your files:
   keys/
   ├── node_private.key   (required, 4032 bytes)
   ├── node_public.key    (required, 1952 bytes)
   └── node_address.json  (required, contains address)

5. Start the node again.

SECURITY NOTES:
- Never share your private key
- Back up your keys securely
- The address is derived from your public key and cannot be changed

For more information, see keys/README.md
================================================================================
`

func (n *Node) LoadKeys() error {
	// Method 1: Load from config file paths
	if n.config.PrivateKeyFile != "" && n.config.PublicKeyFile != "" {
		privKey, err := os.ReadFile(n.config.PrivateKeyFile)
		if err != nil {
			return fmt.Errorf("failed to read private key: %v", err)
		}

		pubKey, err := os.ReadFile(n.config.PublicKeyFile)
		if err != nil {
			return fmt.Errorf("failed to read public key: %v", err)
		}

		n.privateKey = privKey
		n.publicKey = pubKey
		n.address = n.config.NodeAddress
		return nil
	}

	// Method 2: Load from keys/ directory
	privKeyPath := filepath.Join("keys", "node_private.key")
	pubKeyPath := filepath.Join("keys", "node_public.key")
	addrPath := filepath.Join("keys", "node_address.json")

	// Check if private key exists
	if _, err := os.Stat(privKeyPath); os.IsNotExist(err) {
		fmt.Print(keysSetupHelp)
		return fmt.Errorf("keys not configured: %s not found", privKeyPath)
	}

	// Check if public key exists
	if _, err := os.Stat(pubKeyPath); os.IsNotExist(err) {
		fmt.Print(keysSetupHelp)
		return fmt.Errorf("keys not configured: %s not found", pubKeyPath)
	}

	// Check if address file exists
	if _, err := os.Stat(addrPath); os.IsNotExist(err) {
		fmt.Print(keysSetupHelp)
		return fmt.Errorf("keys not configured: %s not found", addrPath)
	}

	// Read private key
	privKey, err := os.ReadFile(privKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read private key: %v", err)
	}

	// Read public key
	pubKey, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read public key: %v", err)
	}

	// Read and parse address
	addrData, err := os.ReadFile(addrPath)
	if err != nil {
		return fmt.Errorf("failed to read address file: %v", err)
	}

	var addrJSON map[string]string
	if err := json.Unmarshal(addrData, &addrJSON); err != nil {
		return fmt.Errorf("failed to parse address JSON: %v", err)
	}

	address := addrJSON["address"]
	if address == "" || strings.HasPrefix(address, "YOUR_") || strings.HasPrefix(address, "<") {
		fmt.Print(keysSetupHelp)
		return fmt.Errorf("address not configured: please edit %s and set your address", addrPath)
	}

	// Validate address matches public key
	derivedAddress := core.DeriveAddress(pubKey)
	if address != derivedAddress {
		return fmt.Errorf("address mismatch: configured address %s does not match public key (expected %s)", address, derivedAddress)
	}

	n.privateKey = privKey
	n.publicKey = pubKey
	n.address = address

	return nil
}
