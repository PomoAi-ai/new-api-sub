package secret

import (
	"crypto/rand"
	"errors"
	"testing"
)

func box(t *testing.T) *Box {
	t.Helper()
	b, err := New([]byte(rand.Text() + rand.Text()))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOpenRefusesWhatItWasNotSealedFor(t *testing.T) {
	a, other := box(t), box(t)
	sealed := a.TOTP.Seal("totp:portal_users:1", []byte("value"))

	if got, err := a.TOTP.Open("totp:portal_users:1", sealed); err != nil || string(got) != "value" {
		t.Fatalf("round trip: %q, %v", got, err)
	}
	if _, err := a.TOTP.Open("totp:portal_users:2", sealed); !errors.Is(err, ErrCorrupt) {
		t.Errorf("another account: %v", err)
	}
	if _, err := a.TOTP.Open("totp:admin_users:1", sealed); !errors.Is(err, ErrCorrupt) {
		t.Errorf("another realm: %v", err)
	}
	if _, err := a.NewAPI.Open("totp:portal_users:1", sealed); !errors.Is(err, ErrWrongKey) {
		t.Errorf("another subkey: %v", err)
	}
	if _, err := other.TOTP.Open("totp:portal_users:1", sealed); !errors.Is(err, ErrWrongKey) {
		t.Errorf("another master key: %v", err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := a.TOTP.Open("totp:portal_users:1", sealed); !errors.Is(err, ErrCorrupt) {
		t.Errorf("tampered: %v", err)
	}
}

func TestChannelKeysStayWithTheirChannel(t *testing.T) {
	b := box(t)
	sealed := b.ChannelKey.Seal("channel:10001:key", []byte("sk-value"))
	if got, err := b.ChannelKey.Open("channel:10001:key", sealed); err != nil || string(got) != "sk-value" {
		t.Fatalf("round trip: %q, %v", got, err)
	}
	if _, err := b.ChannelKey.Open("channel:10002:key", sealed); !errors.Is(err, ErrCorrupt) {
		t.Errorf("another channel: %v", err)
	}
	// A pool secret cannot be read with a channel key's field, nor the other way.
	if _, err := b.NewAPI.Open("channel:10001:key", sealed); !errors.Is(err, ErrWrongKey) {
		t.Errorf("pool subkey: %v", err)
	}
	if _, err := b.ChannelKey.Open("channel:10001:key", b.NewAPI.Seal("channel:10001:key", []byte("x"))); !errors.Is(err, ErrWrongKey) {
		t.Errorf("pool ciphertext: %v", err)
	}
}

func TestNewRefusesAShortMasterKey(t *testing.T) {
	if _, err := New(make([]byte, 31)); err == nil {
		t.Error("31 bytes accepted")
	}
}
