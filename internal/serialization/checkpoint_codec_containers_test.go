package serialization

import (
	"reflect"
	"testing"
)

type checkpointContainerNative struct{ text string }

func TestCheckpointCodecConcreteContainersRoundTrip(t *testing.T) {
	err := RegisterCheckpointCodec(CheckpointCodec[*checkpointContainerNative, map[string]string]{
		Encode: func(v *checkpointContainerNative) (map[string]string, error) {
			if v == nil {
				return nil, nil
			}
			return map[string]string{"text": v.text}, nil
		},
		Decode: func(v map[string]string) (*checkpointContainerNative, error) {
			if v == nil {
				return nil, nil
			}
			return &checkpointContainerNative{text: v["text"]}, nil
		},
		Clone: func(v *checkpointContainerNative) (*checkpointContainerNative, error) { return v, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &InternalSerializer{}
	tests := []struct {
		name string
		in   any
		out  any
	}{
		{"direct", &checkpointContainerNative{text: "value"}, new(*checkpointContainerNative)},
		{"slice", []*checkpointContainerNative{{text: "value"}}, new([]*checkpointContainerNative)},
		{"map", map[string]*checkpointContainerNative{"key": {text: "value"}}, new(map[string]*checkpointContainerNative)},
		{"array", [2]*checkpointContainerNative{{text: "value"}, nil}, new([2]*checkpointContainerNative)},
		{"nested-slice", [][]*checkpointContainerNative{{{text: "value"}, nil}}, new([][]*checkpointContainerNative)},
		{"map-of-slices", map[string][]*checkpointContainerNative{"key": {{text: "value"}, nil}}, new(map[string][]*checkpointContainerNative)},
		{"slice-of-maps", []map[string]*checkpointContainerNative{{"key": {text: "value"}, "nil": nil}}, new([]map[string]*checkpointContainerNative)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire, err := s.Marshal(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Unmarshal(wire, tt.out); err != nil {
				t.Fatalf("native concrete checkpoint restore: %v; wire=%s", err, wire)
			}
			got := reflect.ValueOf(tt.out).Elem().Interface()
			if !reflect.DeepEqual(got, tt.in) {
				t.Fatalf("got=%#v want=%#v", got, tt.in)
			}
		})
	}
}

type checkpointNilNative struct{ Value string }
type checkpointNilWire struct{ Value string }

func TestCheckpointCodecNilRegisteredStructRemainsNil(t *testing.T) {
	if err := GenericRegister[checkpointNilNative]("checkpoint_nil_native"); err != nil {
		t.Fatal(err)
	}
	if err := RegisterCheckpointCodec(CheckpointCodec[*checkpointNilNative, *checkpointNilWire]{
		Encode: func(v *checkpointNilNative) (*checkpointNilWire, error) {
			if v == nil {
				return nil, nil
			}
			return &checkpointNilWire{Value: v.Value}, nil
		},
		Decode: func(v *checkpointNilWire) (*checkpointNilNative, error) {
			if v == nil {
				return nil, nil
			}
			return &checkpointNilNative{Value: v.Value}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	s := &InternalSerializer{}
	var source *checkpointNilNative
	wire, err := s.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var output *checkpointNilNative
	if err = s.Unmarshal(wire, &output); err != nil {
		t.Fatal(err)
	}
	if output != nil {
		t.Fatalf("typed nil became nonnil: %#v wire=%s", output, wire)
	}
	tree, err := s.MarshalTree(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UnmarshalTree(tree, &output); err != nil {
		t.Fatal(err)
	}
	if output != nil {
		t.Fatalf("typed nil Tree became nonnil: %#v", output)
	}
}
