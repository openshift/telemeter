package rand

// NumberGenerator defines an interface to generate numbers.
type NumberGenerator interface {
	Uint64() uint64
	TwoUint64() (uint64, uint64)
}
