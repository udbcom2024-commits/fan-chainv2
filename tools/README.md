# FAN Chain Tools

Quantum-safe key generation and utility tools for FAN blockchain.

## Key Generator (keygen.go)

Generates ML-DSA-65 quantum-safe keypairs and FAN chain addresses.

### Features

- **NIST Level 3 Security**: 256-bit quantum security
- **ML-DSA-65**: Post-quantum digital signature algorithm
- **Base36 Addresses**: 37-character addresses (F + 36 base36 chars)
- **Checksum Validation**: 4-byte checksum for error detection
- **SHA3-256**: Quantum-resistant hashing

### Installation

```bash
cd tools
go mod download
```

### Usage

#### Generate Genesis Keys

```bash
go run keygen.go -o ../addr/genesis -n genesis
```

#### Generate Custom Keys

```bash
go run keygen.go -o ../addr/mykeys -n myaccount
```

### Command Line Options

- `-o <directory>`: Output directory for keys (default: `../addr/genesis`)
- `-n <name>`: Key pair name prefix (default: `genesis`)

### Output Files

The tool generates 4 files:

1. **`<name>_private.key`**: ML-DSA-65 private key (4032 bytes)
   - **KEEP SECURE!** Never share or commit to git
   - File permissions: 0600 (owner read/write only)

2. **`<name>_public.key`**: ML-DSA-65 public key (1952 bytes)
   - Safe to share publicly
   - File permissions: 0644

3. **`<name>_address.txt`**: FAN chain address (37 characters)
   - Format: `F` + 36 base36 characters (0-9, a-z)
   - Example: `Fk3m8x2p9q7w5n1c4v8b2z6y3r5t9j4a8d2f6`
   - Includes 4-byte checksum for validation

4. **`<name>_info.txt`**: Key information summary
   - Address, algorithm, security level
   - Key sizes and public key hash
   - Generation timestamp

### Address Format

FAN chain addresses are 37 characters:

```
Format: F[36 base36 characters]
Characters: 0-9, a-z (all lowercase)
Structure:
  - 1 byte: Prefix 'F'
  - 20 bytes: SHA3-256(PublicKey)[:20]
  - 4 bytes: Checksum
  - Encoded: Base36
```

### Example Output

```
FAN Chain Quantum-Safe Key Generator
=====================================
Security: NIST Level 3 (256-bit)
Algorithm: ML-DSA-65
Address: Base36, 37 characters

Generating ML-DSA-65 keypair...
✓ Address generated: Fk3m8x2p9q7w5n1c4v8b2z6y3r5t9j4a8d2f6
✓ Address validation passed
✓ Private key saved: ../addr/genesis/genesis_private.key
✓ Public key saved: ../addr/genesis/genesis_public.key
✓ Address saved: ../addr/genesis/genesis_address.txt
✓ Key info saved: ../addr/genesis/genesis_info.txt

=====================================
✓ Key generation completed successfully!

IMPORTANT: Keep your private key secure!
Private key: ../addr/genesis/genesis_private.key
Genesis address: Fk3m8x2p9q7w5n1c4v8b2z6y3r5t9j4a8d2f6
```

### Security Notes

1. **Private keys** are saved with 0600 permissions (owner only)
2. All keys use **cryptographically secure random** number generation
3. The `addr/` directory is **excluded from git** via `.gitignore`
4. **Never share** private keys or commit them to version control
5. Addresses include **checksums** to prevent typos and corruption

### Technical Details

#### Key Generation Process

```go
// 1. Generate ML-DSA-65 keypair
publicKey, privateKey, err := mldsa65.GenerateKey(rand.Reader)

// 2. Hash public key with SHA3-256
hash := sha3.Sum256(publicKeyBytes)

// 3. Take first 20 bytes
addressData := hash[:20]

// 4. Calculate checksum
checksum := sha3.Sum256(addressData)[:4]

// 5. Combine and encode to Base36
fullData := addressData + checksum  // 24 bytes
address := "F" + base36Encode(fullData)  // 37 chars
```

#### Address Validation

```go
func validateAddress(addr string) bool {
    // 1. Check length (37)
    // 2. Check prefix ('F')
    // 3. Check characters (0-9a-z)
    // 4. Decode Base36
    // 5. Verify checksum
}
```

### Dependencies

- `github.com/cloudflare/circl` - Cloudflare's cryptographic library
  - Provides ML-DSA-65 (Dilithium) implementation
  - NIST-approved post-quantum cryptography

### Quantum Security

ML-DSA-65 provides security against:
- **Classical computers**: > 256-bit security
- **Quantum computers**: NIST Level 3 (AES-192 equivalent)

This ensures FAN chain remains secure even when quantum computers become practical.

## License

Open source component of FAN blockchain project.
