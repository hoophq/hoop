package clientkeysstorage

import (
	"testing"
)

func TestMustMatchHashFuncs(t *testing.T) {
	secretKey, wantSecretKeyHash, err := generateSecureRandomKey()
	if err != nil {
		t.Fatalf("did not expect error on generating random key: %v", err)
	}

	gotSecretKeyHash, err := hash256Key(secretKey)
	if err != nil {
		t.Fatalf("did not expect error on hashing secret key: %v", err)
	}

	if wantSecretKeyHash != gotSecretKeyHash {
		t.Errorf("expected secrets to match, want=%v, got=%v", wantSecretKeyHash, gotSecretKeyHash)
	}
}

func TestMustNotMatchHashFuncs(t *testing.T) {
	_, wantSecretKeyHash, err := generateSecureRandomKey()
	if err != nil {
		t.Fatalf("did not expect error on generating random key: %v", err)
	}

	gotSecretKeyHash, err := hash256Key("different-secret")
	if err != nil {
		t.Fatalf("did not expect error on hashing secret key: %v", err)
	}

	if wantSecretKeyHash == gotSecretKeyHash {
		t.Errorf("expected secrets not to match, want=%v, got=%v", wantSecretKeyHash, gotSecretKeyHash)
	}
}
