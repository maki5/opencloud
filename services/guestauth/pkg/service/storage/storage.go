package storage

import (
	"errors"
	"time"
)

// ErrNotFound is returned when a record does not exist in the storage.
var ErrNotFound = errors.New("record not found")

// Record holds the data persisted for a guest share token.
type Record struct {
	ShareID     string    `json:"shareid"`
	ShareIDHash string    `json:"shareidhash"`
	SecretHash  string    `json:"secrethash"`
	Expiry      time.Time `json:"expiry,omitzero"`
	Redeemed    bool      `json:"redeemed"`
}

// Storage is the interface for persisting token records. Implementations need
// to be safe for concurrent use.
type Storage interface {
	Add(rec Record) error
	Get(shareIDHash string) (Record, error)
	Remove(shareIDHash string) error
	Redeem(shareIDHash string) error
}
