package util

import (
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// NewUUIDv7String returns a time-ordered UUIDv7 string for index-friendly keys.
func NewUUIDv7String() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// MustUUIDv7String returns a UUIDv7 string or panics on failure.
func MustUUIDv7String() string {
	id, err := NewUUIDv7String()
	if err != nil {
		panic(err)
	}
	return id
}

// NewObjectIDHex returns a MongoDB ObjectID hex string (12-byte, time-ordered).
func NewObjectIDHex() string {
	return bson.NewObjectID().Hex()
}

// NewObjectID returns a MongoDB ObjectID value for binary storage.
func NewObjectID() bson.ObjectID {
	return bson.NewObjectID()
}
