package secrets

import "testing"

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
	blob, _ := Encrypt("secret")
	b := []byte(blob)
	b[len(b)-1] ^= 0x01
	if _, err := Decrypt(string(b)); err == nil {
		t.Fatal("expected decrypt to fail on tampered blob")
	}
}
