package serialization

import (
	"reflect"
	"testing"
)

type codecNative struct{ private string }
type codecHolder struct {
	Frame   *codecNative
	Dynamic any
	Frames  []*codecNative
}

func TestCheckpointCodecWireAndNativeTree(t *testing.T) {
	calls := 0
	err := RegisterCheckpointCodec(CheckpointCodec[*codecNative, map[string]string]{
		Encode: func(v *codecNative) (map[string]string, error) {
			calls++
			if v == nil {
				return nil, nil
			}
			return map[string]string{"value": v.private}, nil
		},
		Decode: func(v map[string]string) (*codecNative, error) {
			calls++
			if v == nil {
				return nil, nil
			}
			return &codecNative{private: v["value"]}, nil
		},
		Clone: func(v *codecNative) (*codecNative, error) { return v, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &InternalSerializer{}
	frame := &codecNative{private: "owned"}
	input := codecHolder{Frame: frame, Dynamic: frame, Frames: []*codecNative{frame}}
	tree, err := s.MarshalTree(input)
	if err != nil {
		t.Fatal(err)
	}
	var out codecHolder
	if err = s.UnmarshalTree(tree, &out); err != nil {
		t.Fatal(err)
	}
	if out.Frame != frame || out.Dynamic != frame || out.Frames[0] != frame || calls != 0 {
		t.Fatalf("tree lost native ownership: %#v calls=%d", out, calls)
	}
	data, err := s.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	historical, err := s.Marshal(map[string]string{"value": "owned"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(data, historical) {
		t.Fatalf("wire changed: %s != %s", data, historical)
	}
	var restored *codecNative
	if err = s.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.private != "owned" {
		t.Fatalf("private native lost: %#v", restored)
	}
}
