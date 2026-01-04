// Package crypto provides encryption and decryption utilities for the Moonlight streaming protocol.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
)

var (
	// ErrInvalidKey indicates an invalid key size
	ErrInvalidKey = errors.New("invalid key size")
	// ErrDecryptionFailed indicates decryption failed
	ErrDecryptionFailed = errors.New("decryption failed")
	// ErrEncryptionFailed indicates encryption failed
	ErrEncryptionFailed = errors.New("encryption failed")
)

// Context holds encryption/decryption state
type Context struct {
	key       []byte
	gcmCipher cipher.AEAD
	cbcBlock  cipher.Block
}

// NewContext creates a new crypto context with the given AES key
func NewContext(key []byte) (*Context, error) {
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, ErrInvalidKey
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	return &Context{
		key:       key,
		gcmCipher: gcm,
		cbcBlock:  block,
	}, nil
}

// EncryptGCM encrypts data using AES-GCM
func (c *Context) EncryptGCM(plaintext, iv, additionalData []byte) (ciphertext, tag []byte, err error) {
	if c.gcmCipher == nil {
		return nil, nil, ErrEncryptionFailed
	}

	// Ensure IV is correct size
	if len(iv) != c.gcmCipher.NonceSize() {
		return nil, nil, errors.New("invalid IV size")
	}

	// Encrypt with GCM (returns ciphertext with tag appended)
	sealed := c.gcmCipher.Seal(nil, iv, plaintext, additionalData)

	// Split ciphertext and tag
	tagStart := len(sealed) - c.gcmCipher.Overhead()
	ciphertext = sealed[:tagStart]
	tag = sealed[tagStart:]

	return ciphertext, tag, nil
}

// DecryptGCM decrypts data using AES-GCM
func (c *Context) DecryptGCM(ciphertext, iv, tag, additionalData []byte) ([]byte, error) {
	if c.gcmCipher == nil {
		return nil, ErrDecryptionFailed
	}

	// Ensure IV is correct size
	if len(iv) != c.gcmCipher.NonceSize() {
		return nil, errors.New("invalid IV size")
	}

	// Combine ciphertext and tag for decryption
	sealed := make([]byte, len(ciphertext)+len(tag))
	copy(sealed, ciphertext)
	copy(sealed[len(ciphertext):], tag)

	// Decrypt
	plaintext, err := c.gcmCipher.Open(nil, iv, sealed, additionalData)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}

// EncryptCBC encrypts data using AES-CBC with PKCS7 padding
func (c *Context) EncryptCBC(plaintext, iv []byte) ([]byte, error) {
	if c.cbcBlock == nil {
		return nil, ErrEncryptionFailed
	}

	blockSize := c.cbcBlock.BlockSize()

	// Ensure IV is correct size
	if len(iv) != blockSize {
		return nil, errors.New("invalid IV size")
	}

	// Apply PKCS7 padding
	padding := blockSize - (len(plaintext) % blockSize)
	paddedPlaintext := make([]byte, len(plaintext)+padding)
	copy(paddedPlaintext, plaintext)
	for i := len(plaintext); i < len(paddedPlaintext); i++ {
		paddedPlaintext[i] = byte(padding)
	}

	// Encrypt
	ciphertext := make([]byte, len(paddedPlaintext))
	mode := cipher.NewCBCEncrypter(c.cbcBlock, iv)
	mode.CryptBlocks(ciphertext, paddedPlaintext)

	return ciphertext, nil
}

// DecryptCBC decrypts data using AES-CBC and removes PKCS7 padding
func (c *Context) DecryptCBC(ciphertext, iv []byte) ([]byte, error) {
	if c.cbcBlock == nil {
		return nil, ErrDecryptionFailed
	}

	blockSize := c.cbcBlock.BlockSize()

	// Ensure IV is correct size
	if len(iv) != blockSize {
		return nil, errors.New("invalid IV size")
	}

	// Ensure ciphertext is multiple of block size
	if len(ciphertext)%blockSize != 0 {
		return nil, errors.New("invalid ciphertext size")
	}

	// Decrypt
	plaintext := make([]byte, len(ciphertext))
	mode := cipher.NewCBCDecrypter(c.cbcBlock, iv)
	mode.CryptBlocks(plaintext, ciphertext)

	// Remove PKCS7 padding
	if len(plaintext) > 0 {
		padding := int(plaintext[len(plaintext)-1])
		if padding > 0 && padding <= blockSize {
			// Verify padding
			valid := true
			for i := len(plaintext) - padding; i < len(plaintext); i++ {
				if plaintext[i] != byte(padding) {
					valid = false
					break
				}
			}
			if valid {
				plaintext = plaintext[:len(plaintext)-padding]
			}
		}
	}

	return plaintext, nil
}

// EncryptCBCPadToBlock encrypts data with padding to exactly one block
// This is used for input stream encryption where we want deterministic output size
func (c *Context) EncryptCBCPadToBlock(plaintext, iv []byte) ([]byte, error) {
	if c.cbcBlock == nil {
		return nil, ErrEncryptionFailed
	}

	blockSize := c.cbcBlock.BlockSize()

	// Pad to block size
	paddedSize := ((len(plaintext) + blockSize - 1) / blockSize) * blockSize
	if paddedSize == 0 {
		paddedSize = blockSize
	}

	paddedPlaintext := make([]byte, paddedSize)
	copy(paddedPlaintext, plaintext)

	// Encrypt
	ciphertext := make([]byte, paddedSize)
	mode := cipher.NewCBCEncrypter(c.cbcBlock, iv)
	mode.CryptBlocks(ciphertext, paddedPlaintext)

	return ciphertext, nil
}

// GCMNonceSize returns the required nonce size for GCM encryption
func (c *Context) GCMNonceSize() int {
	if c.gcmCipher != nil {
		return c.gcmCipher.NonceSize()
	}
	return 12 // Standard GCM nonce size
}

// GCMOverhead returns the authentication tag overhead for GCM encryption
func (c *Context) GCMOverhead() int {
	if c.gcmCipher != nil {
		return c.gcmCipher.Overhead()
	}
	return 16 // Standard GCM tag size
}

// BlockSize returns the AES block size
func (c *Context) BlockSize() int {
	if c.cbcBlock != nil {
		return c.cbcBlock.BlockSize()
	}
	return 16 // AES block size
}
