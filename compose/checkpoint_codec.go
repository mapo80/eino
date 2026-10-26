package compose

import "github.com/cloudwego/eino/internal/serialization"

// CheckpointCodec explicitly separates a value's durable wire from its
// in-memory clone. Encode and Decode only run at the byte checkpoint boundary.
// Clone, when set, supplies independently owned values to in-memory checkpoint
// trees; immutable values can return the same value. With Clone nil the SDK
// recursively clones fields and containers, preserving their ownership.
type CheckpointCodec[T, Wire any] struct {
	Encode func(T) (Wire, error)
	Decode func(Wire) (T, error)
	Clone  func(T) (T, error)
}

// RegisterCheckpointCodec registers a type's codec before compiling graphs.
// The byte representation remains exactly Wire's existing representation;
// no new wire marker is added. Duplicate registrations are refused.
func RegisterCheckpointCodec[T, Wire any](codec CheckpointCodec[T, Wire]) error {
	return serialization.RegisterCheckpointCodec(serialization.CheckpointCodec[T, Wire]{Encode: codec.Encode, Decode: codec.Decode, Clone: codec.Clone})
}
