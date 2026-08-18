package cryptox

import "testing"

func TestSealOpenRoundTrip(t *testing.T) {
	var key [keySize]byte
	for i := range key {
		key[i] = byte(i)
	}

	plaintext := []byte("super-secret-refresh-token")
	sealed, err := Seal(&key, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	got, err := Open(&key, sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("round trip mismatch: got %q want %q", got, plaintext)
	}
}

func TestOpenWrongKeyFails(t *testing.T) {
	var key1, key2 [keySize]byte
	key2[0] = 1

	sealed, err := Seal(&key1, []byte("data"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if _, err := Open(&key2, sealed); err == nil {
		t.Fatal("expected Open with wrong key to fail")
	}
}

func TestDeriveKeyDeterministic(t *testing.T) {
	salt, err := NewSalt()
	if err != nil {
		t.Fatalf("NewSalt: %v", err)
	}

	k1 := DeriveKey("correct horse battery staple", salt)
	k2 := DeriveKey("correct horse battery staple", salt)
	if *k1 != *k2 {
		t.Fatal("DeriveKey should be deterministic for the same passphrase and salt")
	}

	k3 := DeriveKey("different passphrase", salt)
	if *k1 == *k3 {
		t.Fatal("DeriveKey should differ for different passphrases")
	}
}
