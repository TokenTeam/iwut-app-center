package generator

import "crypto/rand"

type GreyCohortSeedGenerator struct{}

func NewGreyCohortSeedGenerator() *GreyCohortSeedGenerator       { return &GreyCohortSeedGenerator{} }
func (*GreyCohortSeedGenerator) Read(buffer []byte) (int, error) { return rand.Read(buffer) }
