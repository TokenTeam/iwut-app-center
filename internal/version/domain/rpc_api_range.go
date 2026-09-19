package domain

type RPCApiRange struct {
	minimum          int32
	maximumExclusive int32
}

func NewRPCApiRange(minimum, maximumExclusive int32) (RPCApiRange, error) {
	if minimum < 1 || maximumExclusive <= minimum {
		return RPCApiRange{}, ErrInvalidRPCApiRange
	}
	return RPCApiRange{minimum: minimum, maximumExclusive: maximumExclusive}, nil
}

func (apiRange RPCApiRange) Minimum() int32          { return apiRange.minimum }
func (apiRange RPCApiRange) MaximumExclusive() int32 { return apiRange.maximumExclusive }

func (apiRange RPCApiRange) valid() bool {
	return apiRange.minimum >= 1 && apiRange.maximumExclusive > apiRange.minimum
}
