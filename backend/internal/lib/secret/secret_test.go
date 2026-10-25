package secret

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func newBox(t *testing.T) *Box {
	t.Helper()
	key := make([]byte, 32)
	rand.Read(key)
	b, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpenRoundTrip(t *testing.T) {
	b := newBox(t)
	plain := []byte("postgres://app:s3cret@db/app")
	ct, err := b.Seal(plain, []byte("target-dsn"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("s3cret")) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := b.Open(ct, []byte("target-dsn"))
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip: %q %v", got, err)
	}
	again, _ := b.Seal(plain, []byte("target-dsn"))
	if bytes.Equal(ct, again) {
		t.Error("two encryptions of the same value must differ (random nonce)")
	}
}

func TestOpenRejectsTamperingWrongKeyAndContext(t *testing.T) {
	b := newBox(t)
	ct, _ := b.Seal([]byte("value"), []byte("ctx"))
	tampered := bytes.Clone(ct)
	tampered[len(tampered)-1] ^= 1
	other := newBox(t)
	for name, try := range map[string]func() error{
		"tampered":      func() error { _, err := b.Open(tampered, []byte("ctx")); return err },
		"wrong key":     func() error { _, err := other.Open(ct, []byte("ctx")); return err },
		"wrong context": func() error { _, err := b.Open(ct, []byte("other")); return err },
		"truncated":     func() error { _, err := b.Open(ct[:5], []byte("ctx")); return err },
		"bad version":   func() error { c := bytes.Clone(ct); c[0] = 9; _, err := b.Open(c, []byte("ctx")); return err },
	} {
		if err := try(); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: got %v, want ErrDecrypt", name, err)
		}
	}
}

func TestParseKey(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawURLEncoding} {
		got, err := ParseKey(" " + enc.EncodeToString(key) + "\n")
		if err != nil || !bytes.Equal(got, key) {
			t.Errorf("ParseKey(%v) = %v", enc, err)
		}
	}
	for _, bad := range []string{"", "not base64 !!!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("ParseKey(%q) accepted", bad)
		}
	}
}
