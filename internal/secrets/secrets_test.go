package secrets

import (
	"encoding/base64"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	t.Setenv("DRUMDROP_CONFIG_DIR", t.TempDir())
	blob, err := Encrypt("hunter2:correct horse")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decrypt(blob)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hunter2:correct horse" {
		t.Fatalf("Decrypt = %q", got)
	}
}

func TestTamperFails(t *testing.T) {
	t.Setenv("DRUMDROP_CONFIG_DIR", t.TempDir())
	blob, err := Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0x01 // flip a bit in the last byte (inside the GCM auth tag)
	if _, err := Decrypt(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("expected decrypt to fail on tampered ciphertext (auth tag)")
	}
}
