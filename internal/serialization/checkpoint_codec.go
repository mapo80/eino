package serialization

import (
	"fmt"
	"reflect"
	"sync"
)

// CheckpointCodec describes the explicit wire boundary of a value. Clone is
// optional: when present, it owns the value retained by an in-memory Tree and
// returned by each Tree read. Immutable values may return themselves. When
// absent the serializer recursively copies the value as it did before.
// Encode and Decode are only called by byte serialization, never by Tree
// cloning. They must not mutate the input or retain mutable borrowed data.
type CheckpointCodec[T, Wire any] struct {
	Encode func(T) (Wire, error)
	Decode func(Wire) (T, error)
	Clone  func(T) (T, error)
}

type checkpointCodec struct {
	wire   reflect.Type
	encode func(any) (any, error)
	decode func(any) (any, error)
	clone  func(any) (any, error)
}

var checkpointCodecs = struct {
	sync.RWMutex
	values map[reflect.Type]checkpointCodec
}{values: map[reflect.Type]checkpointCodec{}}

// RegisterCheckpointCodec installs a codec before graphs using the type are
// built. Existing registrations are never replaced. Registration does not
// change other types or introduce a wire tag: the wire is exactly Wire's
// existing checkpoint representation.
func RegisterCheckpointCodec[T, Wire any](codec CheckpointCodec[T, Wire]) error {
	if codec.Encode == nil || codec.Decode == nil {
		return fmt.Errorf("checkpoint codec requires encode and decode")
	}
	native := reflect.TypeOf((*T)(nil)).Elem()
	wire := reflect.TypeOf((*Wire)(nil)).Elem()
	checkpointCodecs.Lock()
	defer checkpointCodecs.Unlock()
	if _, exists := checkpointCodecs.values[native]; exists {
		return fmt.Errorf("checkpoint codec already registered: %s", native)
	}
	c := checkpointCodec{wire: wire,
		encode: func(v any) (any, error) { return codec.Encode(v.(T)) },
		decode: func(v any) (any, error) {
			if v == nil {
				var zero Wire
				return codec.Decode(zero)
			}
			return codec.Decode(v.(Wire))
		},
	}
	if codec.Clone != nil {
		c.clone = func(v any) (any, error) { return codec.Clone(v.(T)) }
	}
	checkpointCodecs.values[native] = c
	return nil
}

func codecFor(t reflect.Type) (checkpointCodec, bool) {
	checkpointCodecs.RLock()
	codec, ok := checkpointCodecs.values[t]
	checkpointCodecs.RUnlock()
	return codec, ok
}
