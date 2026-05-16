package identity_test

import (
	"fmt"
	"os"
	"time"

	"hop.top/kit/go/core/identity"
)

func ExampleGenerate() {
	kp, err := identity.Generate()
	if err != nil {
		panic(err)
	}

	fmt.Println(len(kp.PublicKey) > 0)
	fmt.Println(len(kp.PrivateKey) > 0)
	// Output:
	// true
	// true
}

func ExampleKeypair_SignJWT() {
	kp, _ := identity.Generate()

	token, err := kp.SignJWT(identity.Claims{
		Subject:   kp.PublicKeyID(),
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		panic(err)
	}

	claims, err := identity.VerifyJWT(token, kp.PublicKey)
	if err != nil {
		panic(err)
	}

	fmt.Println(claims.Subject == kp.PublicKeyID())
	// Output: true
}

func ExampleKeypair_DeriveKey() {
	kp, _ := identity.Generate()

	key := kp.DeriveKey()
	plaintext := []byte("secret data")

	ciphertext, err := identity.Encrypt(key, plaintext)
	if err != nil {
		panic(err)
	}

	decrypted, err := identity.Decrypt(key, ciphertext)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(decrypted))
	// Output: secret data
}

func ExampleEncrypt() {
	kp, _ := identity.Generate()
	key := kp.DeriveKey()

	ciphertext, _ := identity.Encrypt(key, []byte("hello"))
	plaintext, _ := identity.Decrypt(key, ciphertext)

	fmt.Println(string(plaintext))
	// Output: hello
}

func ExampleStore_LoadOrGenerate() {
	dir, _ := os.MkdirTemp("", "identity-example-*")
	defer os.RemoveAll(dir)

	store, err := identity.NewStore(dir)
	if err != nil {
		panic(err)
	}

	kp1, _ := store.LoadOrGenerate()
	kp2, _ := store.LoadOrGenerate()

	fmt.Println(kp1.PublicKeyID() == kp2.PublicKeyID())
	// Output: true
}
