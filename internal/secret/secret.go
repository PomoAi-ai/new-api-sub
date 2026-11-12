// Package secret encrypts what the system must read back, under keys derived
// from APP_MASTER_KEY. A ciphertext is bound to its field, so one cannot stand
// in for another. Rotating the master key is not supported.
package secret

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

const (
	format   = 1
	idLen    = 4
	nonceLen = 12
	header   = 1 + idLen + nonceLen
)

var (
	// ErrWrongKey means another master key wrote the value.
	ErrWrongKey = errors.New("encrypted under another master key")
	// ErrCorrupt means the value was damaged or belongs to another field.
	ErrCorrupt = errors.New("ciphertext is damaged or belongs to another field")
)

// Key encrypts one kind of value.
type Key struct {
	aead cipher.AEAD
	id   [idLen]byte
}

// Box holds the subkeys of a master key. TOTP fields are totp:<realm>:<id>, so
// two accounts cannot swap secrets; NewAPI fields are pool:<id>:<column> (a
// pool copied from before pools: newapi.<column>); ChannelKey fields are
// channel:<id>:key.
type Box struct{ TOTP, NewAPI, ChannelKey *Key }

// New derives the subkeys.
func New(master []byte) (*Box, error) {
	if len(master) < 32 {
		return nil, errors.New("master key must be at least 32 bytes")
	}
	totp, err := derive(master, "totp")
	if err != nil {
		return nil, err
	}
	newapi, err := derive(master, "newapi")
	if err != nil {
		return nil, err
	}
	channelKey, err := derive(master, "channelkey")
	if err != nil {
		return nil, err
	}
	return &Box{TOTP: totp, NewAPI: newapi, ChannelKey: channelKey}, nil
}

func derive(master []byte, label string) (*Key, error) {
	raw, err := hkdf.Key(sha256.New, master, nil, "new-api-sub/"+label, 32+idLen)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(raw[:32])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	k := &Key{aead: aead}
	copy(k.id[:], raw[32:])
	return k, nil
}

func (k *Key) aad(field string) []byte {
	return append(append([]byte{format}, k.id[:]...), field...)
}

// Seal returns format|key id|nonce|ciphertext.
func (k *Key) Seal(field string, plain []byte) []byte {
	out := make([]byte, header, header+len(plain)+k.aead.Overhead())
	out[0] = format
	copy(out[1:], k.id[:])
	rand.Read(out[1+idLen : header]) // never fails: crypto/rand crashes instead
	return k.aead.Seal(out, out[1+idLen:header], plain, k.aad(field))
}

// Open reverses Seal for the same field.
func (k *Key) Open(field string, sealed []byte) ([]byte, error) {
	if len(sealed) < header+k.aead.Overhead() || sealed[0] != format {
		return nil, ErrCorrupt
	}
	if !bytes.Equal(sealed[1:1+idLen], k.id[:]) {
		return nil, ErrWrongKey
	}
	plain, err := k.aead.Open(nil, sealed[1+idLen:header], sealed[header:], k.aad(field))
	if err != nil {
		return nil, ErrCorrupt
	}
	return plain, nil
}
