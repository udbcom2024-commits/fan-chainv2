package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
	"golang.org/x/crypto/scrypt"
	"golang.org/x/crypto/sha3"
)

const (
	// Address configuration
	AddressPrefix = "F"
	AddressLength = 37
	ChecksumBytes = 3
	AddressBytes  = 20
)

const base36Chars = "0123456789abcdefghijklmnopqrstuvwxyz"

// Keystore JSON structure
type Keystore struct {
	Address   string       `json:"address"`
	PublicKey string       `json:"publickey"`
	Crypto    CryptoParams `json:"crypto"`
	ID        string       `json:"id"`
	Version   int          `json:"version"`
}

type CryptoParams struct {
	Cipher       string       `json:"cipher"`
	CipherText   string       `json:"ciphertext"`
	CipherParams CipherParams `json:"cipherparams"`
	KDF          string       `json:"kdf"`
	KDFParams    KDFParams    `json:"kdfparams"`
	MAC          string       `json:"mac"`
}

type CipherParams struct {
	IV string `json:"iv"`
}

type KDFParams struct {
	N      int    `json:"n"`
	R      int    `json:"r"`
	P      int    `json:"p"`
	DKLen  int    `json:"dklen"`
	Salt   string `json:"salt"`
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "generate":
		generateCommand()
	case "export":
		exportCommand()
	case "import":
		importCommand()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("FAN Chain Quantum-Safe Key Tool")
	fmt.Println("================================")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  generate    Generate new ML-DSA-65 keypair")
	fmt.Println("  export      Export private key")
	fmt.Println("  import      Import private key")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  keygen generate -o <dir> -n <name>")
	fmt.Println("  keygen export -key <file> -format <hex|keystore> [-password <pwd>] [-o <output>]")
	fmt.Println("  keygen import -format <hex|keystore> -input <file> [-password <pwd>] -n <name> -o <dir>")
}

func generateCommand() {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	outputDir := fs.String("o", "./keys/default", "Output directory for keys")
	name := fs.String("n", "", "Key pair name (required)")
	fs.Parse(os.Args[2:])

	// Validate required parameters
	if *name == "" {
		fmt.Println("错误：-n 参数是必填项，请指定密钥对名称")
		fmt.Println("示例：go run keygen.go generate -o ./keys/mywallet -n wallet")
		os.Exit(1)
	}

	fmt.Println("FAN Chain Quantum-Safe Key Generator")
	fmt.Println("=====================================")
	fmt.Println("Security: NIST Level 3 (256-bit)")
	fmt.Println("Algorithm: ML-DSA-65")
	fmt.Println()

	// Generate keypair
	fmt.Println("Generating ML-DSA-65 keypair...")
	publicKey, privateKey, err := mldsa65.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatalf("Failed to generate keypair: %v", err)
	}

	// Generate address
	address := generateAddress(publicKey)
	fmt.Printf("✓ Address: %s\n", address)

	// Validate
	if !validateAddress(address) {
		log.Fatal("✗ Address validation failed!")
	}

	// Check if directory exists and has files
	if _, err := os.Stat(*outputDir); err == nil {
		// Directory exists, check for existing key files
		privateKeyPath := filepath.Join(*outputDir, *name+"_private.key")
		publicKeyPath := filepath.Join(*outputDir, *name+"_public.key")
		addressPath := filepath.Join(*outputDir, *name+"_address.txt")

		hasExisting := false
		if _, err := os.Stat(privateKeyPath); err == nil {
			hasExisting = true
		}
		if _, err := os.Stat(publicKeyPath); err == nil {
			hasExisting = true
		}
		if _, err := os.Stat(addressPath); err == nil {
			hasExisting = true
		}

		if hasExisting {
			fmt.Printf("⚠ 警告：目录 %s 下已存在同名密钥文件！\n", *outputDir)
			fmt.Println("⚠ 继续操作将会覆盖并永久丢失原有文件！")
			fmt.Println("⚠ 请先备份旧密钥！")
			fmt.Println()
			fmt.Print("确认继续？(输入 Y 或 y 确认，其他任意键取消): ")

			var confirm string
			fmt.Scanln(&confirm)

			if confirm != "Y" && confirm != "y" {
				fmt.Println("操作已取消。")
				os.Exit(0)
			}
			fmt.Println()
		}
	}

	// Create output directory
	if err := os.MkdirAll(*outputDir, 0700); err != nil {
		log.Fatalf("Failed to create directory: %v", err)
	}

	// Save private key
	privateKeyPath := filepath.Join(*outputDir, *name+"_private.key")
	privateKeyBytes, err := privateKey.MarshalBinary()
	if err != nil {
		log.Fatalf("Failed to marshal private key: %v", err)
	}
	if err := os.WriteFile(privateKeyPath, privateKeyBytes, 0600); err != nil {
		log.Fatalf("Failed to save private key: %v", err)
	}
	fmt.Printf("✓ Private key: %s\n", privateKeyPath)

	// Save public key
	publicKeyPath := filepath.Join(*outputDir, *name+"_public.key")
	publicKeyBytes, err := publicKey.MarshalBinary()
	if err != nil {
		log.Fatalf("Failed to marshal public key: %v", err)
	}
	if err := os.WriteFile(publicKeyPath, publicKeyBytes, 0644); err != nil {
		log.Fatalf("Failed to save public key: %v", err)
	}
	fmt.Printf("✓ Public key: %s\n", publicKeyPath)

	// Save address
	addressPath := filepath.Join(*outputDir, *name+"_address.txt")
	if err := os.WriteFile(addressPath, []byte(address), 0644); err != nil {
		log.Fatalf("Failed to save address: %v", err)
	}
	fmt.Printf("✓ Address file: %s\n", addressPath)

	fmt.Println()
	fmt.Println("✓ Key generation completed!")
	fmt.Printf("Address: %s\n", address)
}

func exportCommand() {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	keyFile := fs.String("key", "", "Private key file path (required)")
	format := fs.String("format", "hex", "Export format: hex, keystore")
	password := fs.String("password", "", "Password for keystore encryption")
	output := fs.String("o", "", "Output file (optional, prints to stdout if not specified)")
	fs.Parse(os.Args[2:])

	if *keyFile == "" {
		fmt.Println("Error: -key is required")
		fs.PrintDefaults()
		os.Exit(1)
	}

	// Read private key
	privateKeyBytes, err := os.ReadFile(*keyFile)
	if err != nil {
		log.Fatalf("Failed to read private key: %v", err)
	}

	// Derive public key file path (replace _private.key with _public.key)
	publicKeyFile := strings.Replace(*keyFile, "_private.key", "_public.key", 1)
	publicKeyBytes, err := os.ReadFile(publicKeyFile)
	if err != nil {
		log.Fatalf("Failed to read public key (expected at %s): %v", publicKeyFile, err)
	}

	// Unmarshal public key to generate address
	var publicKey mldsa65.PublicKey
	if err := publicKey.UnmarshalBinary(publicKeyBytes); err != nil {
		log.Fatalf("Failed to unmarshal public key: %v", err)
	}
	address := generateAddress(&publicKey)

	fmt.Printf("Exporting private key for address: %s\n", address)
	fmt.Printf("Format: %s\n", *format)
	fmt.Println()

	var exportData string

	switch *format {
	case "hex":
		exportData = exportHex(privateKeyBytes)
		fmt.Println("Hex Export (8064 characters):")
		fmt.Println(strings.Repeat("=", 60))
		fmt.Println(exportData)
		fmt.Println(strings.Repeat("=", 60))

	case "keystore":
		if *password == "" {
			fmt.Print("Enter password for keystore encryption: ")
			fmt.Scanln(password)
		}
		keystoreJSON, err := exportKeystore(privateKeyBytes, address, *password, publicKeyBytes)
		if err != nil {
			log.Fatalf("Failed to export keystore: %v", err)
		}
		exportData = keystoreJSON
		fmt.Println("Keystore JSON:")
		fmt.Println(strings.Repeat("=", 60))
		fmt.Println(exportData)
		fmt.Println(strings.Repeat("=", 60))

	default:
		log.Fatalf("Unknown format: %s", *format)
	}

	// Save to file if specified
	if *output != "" {
		if err := os.WriteFile(*output, []byte(exportData), 0600); err != nil {
			log.Fatalf("Failed to write output file: %v", err)
		}
		fmt.Printf("\n✓ Exported to: %s\n", *output)
	}
}

func importCommand() {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	format := fs.String("format", "hex", "Import format: hex, keystore")
	input := fs.String("input", "", "Input file or string (required)")
	password := fs.String("password", "", "Password for keystore decryption")
	name := fs.String("n", "imported", "Key pair name")
	outputDir := fs.String("o", "./keys/default", "Output directory")
	fs.Parse(os.Args[2:])

	if *input == "" {
		fmt.Println("Error: -input is required")
		fs.PrintDefaults()
		os.Exit(1)
	}

	fmt.Printf("Importing private key from %s format...\n", *format)

	var privateKeyBytes []byte
	var publicKeyBytes []byte
	var address string
	var err error

	// Read input
	inputData, err := os.ReadFile(*input)
	if err != nil {
		// If file doesn't exist, treat input as the data itself
		inputData = []byte(*input)
	}

	switch *format {
	case "hex":
		privateKeyBytes, err = importHex(string(inputData))
		if err != nil {
			log.Fatalf("Failed to import hex: %v", err)
		}
		fmt.Println("WARNING: Hex format doesn't include public key/address")
		fmt.Println("You need to provide the corresponding public key separately")
		log.Fatal("Hex import not fully supported yet - use keystore format instead")

	case "keystore":
		if *password == "" {
			fmt.Print("Enter password for keystore decryption: ")
			fmt.Scanln(password)
		}
		privateKeyBytes, publicKeyBytes, address, err = importKeystore(inputData, *password)
		if err != nil {
			log.Fatalf("Failed to import keystore: %v", err)
		}

	default:
		log.Fatalf("Unknown format: %s", *format)
	}

	// Validate private key by trying to unmarshal
	var privateKey mldsa65.PrivateKey
	if err := privateKey.UnmarshalBinary(privateKeyBytes); err != nil {
		log.Fatalf("Invalid private key: %v", err)
	}

	// Validate public key if available
	if len(publicKeyBytes) > 0 {
		var publicKey mldsa65.PublicKey
		if err := publicKey.UnmarshalBinary(publicKeyBytes); err != nil {
			log.Fatalf("Invalid public key: %v", err)
		}
	}

	fmt.Printf("✓ Private key imported successfully\n")
	fmt.Printf("Address: %s\n", address)

	// Check if directory exists and has files
	if _, err := os.Stat(*outputDir); err == nil {
		// Directory exists, check for existing key files
		privateKeyPath := filepath.Join(*outputDir, *name+"_private.key")
		publicKeyPath := filepath.Join(*outputDir, *name+"_public.key")
		addressPath := filepath.Join(*outputDir, *name+"_address.txt")

		hasExisting := false
		if _, err := os.Stat(privateKeyPath); err == nil {
			hasExisting = true
		}
		if _, err := os.Stat(publicKeyPath); err == nil {
			hasExisting = true
		}
		if _, err := os.Stat(addressPath); err == nil {
			hasExisting = true
		}

		if hasExisting {
			fmt.Printf("⚠ 警告：目录 %s 下已存在同名密钥文件！\n", *outputDir)
			fmt.Println("⚠ 继续操作将会覆盖并永久丢失原有文件！")
			fmt.Println("⚠ 请先备份旧密钥！")
			fmt.Println()
			fmt.Print("确认继续？(输入 Y 或 y 确认，其他任意键取消): ")

			var confirm string
			fmt.Scanln(&confirm)

			if confirm != "Y" && confirm != "y" {
				fmt.Println("操作已取消。")
				os.Exit(0)
			}
			fmt.Println()
		}
	}

	// Save to files
	if err := os.MkdirAll(*outputDir, 0700); err != nil {
		log.Fatalf("Failed to create directory: %v", err)
	}

	privateKeyPath := filepath.Join(*outputDir, *name+"_private.key")
	if err := os.WriteFile(privateKeyPath, privateKeyBytes, 0600); err != nil {
		log.Fatalf("Failed to save private key: %v", err)
	}
	fmt.Printf("✓ Private key saved: %s\n", privateKeyPath)

	if len(publicKeyBytes) > 0 {
		publicKeyPath := filepath.Join(*outputDir, *name+"_public.key")
		if err := os.WriteFile(publicKeyPath, publicKeyBytes, 0644); err != nil {
			log.Fatalf("Failed to save public key: %v", err)
		}
		fmt.Printf("✓ Public key saved: %s\n", publicKeyPath)
	}

	if address != "" {
		addressPath := filepath.Join(*outputDir, *name+"_address.txt")
		if err := os.WriteFile(addressPath, []byte(address), 0644); err != nil {
			log.Fatalf("Failed to save address: %v", err)
		}
		fmt.Printf("✓ Address saved: %s\n", addressPath)
	}
}

// Export functions
func exportHex(privateKeyBytes []byte) string {
	return hex.EncodeToString(privateKeyBytes)
}

func exportKeystore(privateKeyBytes []byte, address string, password string, publicKeyBytes []byte) (string, error) {
	// Generate salt
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", err
	}

	// Derive key using scrypt
	derivedKey, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		return "", err
	}

	// Generate IV
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}

	// Encrypt using AES-256-CTR
	block, err := aes.NewCipher(derivedKey[:16])
	if err != nil {
		return "", err
	}

	ciphertext := make([]byte, len(privateKeyBytes))
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(ciphertext, privateKeyBytes)

	// Calculate MAC
	mac := sha3.Sum256(append(derivedKey[16:32], ciphertext...))

	// Create keystore structure
	keystore := Keystore{
		Address:   address,
		PublicKey: hex.EncodeToString(publicKeyBytes),
		Crypto: CryptoParams{
			Cipher:     "aes-128-ctr",
			CipherText: hex.EncodeToString(ciphertext),
			CipherParams: CipherParams{
				IV: hex.EncodeToString(iv),
			},
			KDF: "scrypt",
			KDFParams: KDFParams{
				N:     32768,
				R:     8,
				P:     1,
				DKLen: 32,
				Salt:  hex.EncodeToString(salt),
			},
			MAC: hex.EncodeToString(mac[:]),
		},
		ID:      address,
		Version: 1,
	}

	jsonBytes, err := json.MarshalIndent(keystore, "", "  ")
	if err != nil {
		return "", err
	}

	return string(jsonBytes), nil
}

// Import functions
func importHex(hexString string) ([]byte, error) {
	hexString = strings.TrimSpace(hexString)
	return hex.DecodeString(hexString)
}

func importKeystore(keystoreJSON []byte, password string) ([]byte, []byte, string, error) {
	var keystore Keystore
	if err := json.Unmarshal(keystoreJSON, &keystore); err != nil {
		return nil, nil, "", err
	}

	// Decode public key
	publicKeyBytes, err := hex.DecodeString(keystore.PublicKey)
	if err != nil {
		return nil, nil, "", err
	}

	// Decode parameters
	salt, err := hex.DecodeString(keystore.Crypto.KDFParams.Salt)
	if err != nil {
		return nil, nil, "", err
	}

	ciphertext, err := hex.DecodeString(keystore.Crypto.CipherText)
	if err != nil {
		return nil, nil, "", err
	}

	iv, err := hex.DecodeString(keystore.Crypto.CipherParams.IV)
	if err != nil {
		return nil, nil, "", err
	}

	mac, err := hex.DecodeString(keystore.Crypto.MAC)
	if err != nil {
		return nil, nil, "", err
	}

	// Derive key
	derivedKey, err := scrypt.Key([]byte(password), salt,
		keystore.Crypto.KDFParams.N,
		keystore.Crypto.KDFParams.R,
		keystore.Crypto.KDFParams.P,
		keystore.Crypto.KDFParams.DKLen)
	if err != nil {
		return nil, nil, "", err
	}

	// Verify MAC
	calculatedMAC := sha3.Sum256(append(derivedKey[16:32], ciphertext...))
	if hex.EncodeToString(calculatedMAC[:]) != hex.EncodeToString(mac) {
		return nil, nil, "", fmt.Errorf("invalid password or corrupted keystore")
	}

	// Decrypt
	block, err := aes.NewCipher(derivedKey[:16])
	if err != nil {
		return nil, nil, "", err
	}

	plaintext := make([]byte, len(ciphertext))
	stream := cipher.NewCTR(block, iv)
	stream.XORKeyStream(plaintext, ciphertext)

	return plaintext, publicKeyBytes, keystore.Address, nil
}

// Helper functions
func generateAddress(publicKey *mldsa65.PublicKey) string {
	publicKeyBytes, _ := publicKey.MarshalBinary()
	hash := sha3.Sum256(publicKeyBytes)
	addressData := hash[:AddressBytes]
	checksumHash := sha3.Sum256(addressData)
	checksum := checksumHash[:ChecksumBytes]

	fullData := make([]byte, AddressBytes+ChecksumBytes)
	copy(fullData[:AddressBytes], addressData)
	copy(fullData[AddressBytes:], checksum)

	base36String := base36Encode(fullData)
	for len(base36String) < AddressLength-1 {
		base36String = "0" + base36String
	}

	return AddressPrefix + strings.ToLower(base36String)
}

func validateAddress(addr string) bool {
	if len(addr) != AddressLength {
		return false
	}
	if addr[0] != AddressPrefix[0] {
		return false
	}
	for _, c := range addr[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z')) {
			return false
		}
	}

	data := base36Decode(addr[1:])
	if len(data) != AddressBytes+ChecksumBytes {
		return false
	}

	addressData := data[:AddressBytes]
	checksum := data[AddressBytes:]
	expectedChecksumHash := sha3.Sum256(addressData)
	expectedChecksum := expectedChecksumHash[:ChecksumBytes]

	for i := 0; i < ChecksumBytes; i++ {
		if checksum[i] != expectedChecksum[i] {
			return false
		}
	}
	return true
}

func base36Encode(data []byte) string {
	num := new(big.Int).SetBytes(data)
	if num.Sign() == 0 {
		return "0"
	}

	base := big.NewInt(36)
	result := ""
	for num.Sign() > 0 {
		mod := new(big.Int)
		num.DivMod(num, base, mod)
		result = string(base36Chars[mod.Int64()]) + result
	}
	return result
}

func base36Decode(s string) []byte {
	s = strings.ToLower(s)
	num := new(big.Int)
	base := big.NewInt(36)

	for _, c := range s {
		num.Mul(num, base)
		var digit int64
		if c >= '0' && c <= '9' {
			digit = int64(c - '0')
		} else if c >= 'a' && c <= 'z' {
			digit = int64(c-'a') + 10
		} else {
			return nil
		}
		num.Add(num, big.NewInt(digit))
	}

	bytes := num.Bytes()
	if len(bytes) < AddressBytes+ChecksumBytes {
		padded := make([]byte, AddressBytes+ChecksumBytes)
		copy(padded[AddressBytes+ChecksumBytes-len(bytes):], bytes)
		return padded
	}
	return bytes
}