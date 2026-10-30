package store

import "crypto/rand"

// randRead fills b with cryptographically random bytes.
func randRead(b []byte) (int, error) { return rand.Read(b) }
