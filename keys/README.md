# FAN Chain Node Key Configuration

This directory contains the cryptographic keys for your FAN Chain node.

## Required Files

| File | Description | Permissions |
|------|-------------|-------------|
| `node_private.key` | ML-DSA-65 private key (4032 bytes) | 0600 (owner only) |
| `node_public.key` | ML-DSA-65 public key (1952 bytes) | 0644 |
| `node_address.json` | Node address in JSON format | 0644 |

## Setup Instructions

### Step 1: Generate Keys

Use the keygen tool to generate a new keypair:

```bash
cd tools
go run keygen.go generate -o ../keys -n node
```

This creates:
- `node_private.key` - Your private key (KEEP SECRET!)
- `node_public.key` - Your public key
- `node_address.txt` - Your derived address

### Step 2: Configure Address

Edit `node_address.json` and replace `<YOUR_ADDRESS_HERE>` with the address from `node_address.txt`:

```json
{
  "address": "F1234567890abcdefghijklmnopqrstuvwxyz"
}
```

### Step 3: Verify Configuration

Your keys directory should look like:

```
keys/
├── node_private.key   (4032 bytes)
├── node_public.key    (1952 bytes)
├── node_address.json  (contains your address)
├── node_address.txt   (optional, for reference)
└── README.md          (this file)
```

### Step 4: Start Node

```bash
./fan-chain
```

## Security Notes

1. **Never share your private key** - Anyone with your private key can control your funds
2. **Back up your keys** - Store copies in secure, offline locations
3. **Verify address** - The address is mathematically derived from your public key
4. **Set proper permissions** - Private key should only be readable by owner

## Key Algorithm

FAN Chain uses **ML-DSA-65** (NIST FIPS 204), a quantum-resistant digital signature algorithm:
- Security Level: NIST Level 3 (256-bit equivalent)
- Private Key: 4032 bytes
- Public Key: 1952 bytes
- Signature: 3309 bytes

## Troubleshooting

### "keys not configured" error
- Ensure all three files exist in this directory
- Check that `node_address.json` contains a valid address (not placeholder)

### "address mismatch" error
- The address in `node_address.json` must match the one derived from your public key
- Regenerate keys if you're unsure: `go run keygen.go generate -o ../keys -n node`

### Permission denied
- On Linux/Mac: `chmod 600 node_private.key`
- Ensure the node process can read the files
