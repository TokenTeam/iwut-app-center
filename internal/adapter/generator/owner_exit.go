package generator

import "github.com/google/uuid"

func (g *UUIDv7Generator) NewReceiptID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
