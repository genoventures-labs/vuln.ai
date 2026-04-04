package smoke_tests

import (
	"crypto/md5"
	"crypto/sha1"
	"fmt"
	"math/rand"
	"time"
)

// WeakHashUsage shows use of MD5 and SHA1 for sensitive data.
func WeakHashUsage(password string) {
	// DANGER: Using MD5 for password hashing is insecure.
	h1 := md5.New()
	h1.Write([]byte(password))
	fmt.Printf("MD5: %x\n", h1.Sum(nil))

	// DANGER: Using SHA1 is also considered weak.
	h2 := sha1.Sum([]byte(password))
	fmt.Printf("SHA1: %x\n", h2)
}

// InsecureRandomness shows the use of math/rand for security tokens.
func InsecureRandomness() {
	// DANGER: math/rand is not cryptographically secure.
	// Use crypto/rand instead for session tokens or keys.
	rand.Seed(time.Now().UnixNano())
	token := fmt.Sprintf("token_%d", rand.Intn(1000000))
	fmt.Println("Generated token:", token)
}

// HardcodedCryptoKey shows a hardcoded key in the source code.
func HardcodedCryptoKey() {
	// DANGER: Hardcoded encryption key.
	// Keys should be stored in environment variables or a vault.
	key := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}
	fmt.Printf("Using key for 'AES': %v\n", key)
}

func smoke_test_crypto() {
	WeakHashUsage("mysecretpassword")
	InsecureRandomness()
	HardcodedCryptoKey()
}
