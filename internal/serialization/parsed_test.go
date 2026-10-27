package serialization

import (
	"bytes"
	"math"
	"reflect"
	"sync"
	"testing"
)

type parsedNative struct{ text string }

type parsedHolder struct {
	Name    string
	Count   int
	Ratio   float64
	Ptr     *parsedHolder
	Nil     *parsedHolder
	Any     any
	Strings map[string]string
	Values  map[string]any
	Ints    map[int]string
	List    []any
	Array   [2]string
	Empty   map[string]any
	EmptyL  []string
	Native  *parsedNative
	Natives []*parsedNative
	private string
}

var registerParsedTypes sync.Once

func parsedFixtures(t *testing.T) []any {
	registerParsedTypes.Do(func() {
		if err := GenericRegister[parsedHolder]("parsed_test_holder"); err != nil {
			t.Fatal(err)
		}
		err := RegisterCheckpointCodec(CheckpointCodec[*parsedNative, map[string]string]{
			Encode: func(v *parsedNative) (map[string]string, error) {
				if v == nil {
					return nil, nil
				}
				return map[string]string{"text": v.text}, nil
			},
			Decode: func(v map[string]string) (*parsedNative, error) {
				if v == nil {
					return nil, nil
				}
				return &parsedNative{text: v["text"]}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	keys := map[string]any{}
	strs := map[string]string{}
	for i, s := range leafCompatStrings() {
		if i%7 == 0 {
			keys[s] = s
			strs[s] = s
		}
	}
	holder := &parsedHolder{
		Name: "<n>&\xff", Count: -3, Ratio: 1e-300,
		Ptr:     &parsedHolder{Name: "inner", Any: []any{"a", 1.0, nil}},
		Any:     map[string]any{"k": []any{map[string]any{"deep": "v"}, math.MaxFloat64}},
		Strings: strs,
		Values:  keys,
		Ints:    map[int]string{1: "one", -2: "minus two"},
		List:    []any{nil, "x", 2.5, true, map[string]any{}},
		Array:   [2]string{"a", ""},
		Empty:   map[string]any{},
		EmptyL:  []string{},
		Native:  &parsedNative{text: "native"},
		Natives: []*parsedNative{{text: "a"}, nil},
		private: "dropped",
	}
	return []any{holder, *holder, map[string]any{"holder": holder, "nil": nil}, []any{holder, nil, "s"}, "plain", 7.0, nil}
}

func decodeLike(t *testing.T, v any) any {
	if v == nil {
		return new(any)
	}
	return reflect.New(reflect.TypeOf(v)).Interface()
}

// Decoding the parsed form gives what decoding its bytes gives.
func TestUnmarshalParsedEqualsUnmarshal(t *testing.T) {
	s := &InternalSerializer{}
	for i, v := range parsedFixtures(t) {
		data, parsed, err := s.MarshalParsed(v)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := s.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		fromBytes := decodeLike(t, v)
		bytesErr := s.Unmarshal(data, fromBytes)
		fromParsed := decodeLike(t, v)
		parsedErr := s.UnmarshalParsed(parsed, fromParsed)
		if bytesErr != nil || parsedErr != nil {
			if bytesErr == nil || parsedErr == nil {
				t.Fatalf("fixture %d: bytes error %v, parsed error %v", i, bytesErr, parsedErr)
			}
			continue
		}
		fromPlain := decodeLike(t, v)
		if err = s.Unmarshal(plain, fromPlain); err != nil {
			t.Fatalf("fixture %d: %v", i, err)
		}
		if !reflect.DeepEqual(fromBytes, fromPlain) {
			t.Fatalf("fixture %d: MarshalParsed bytes decode differently from Marshal bytes", i)
		}
		if !reflect.DeepEqual(fromBytes, fromParsed) {
			t.Fatalf("fixture %d: parsed decodes to\n%#v\nbytes decode to\n%#v", i, fromParsed, fromBytes)
		}
	}
}

// The parsed form shares no memory with the marshaled value or with any
// decoded value, and concurrent decodes do not race.
func TestParsedIsolatedFromWriterAndReaders(t *testing.T) {
	s := &InternalSerializer{}
	holder := parsedFixtures(t)[0].(*parsedHolder)
	data, parsed, err := s.MarshalParsed(holder)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), data...)

	holder.Name = "changed"
	holder.Ptr.Any.([]any)[0] = "changed"
	holder.Any.(map[string]any)["k"].([]any)[0].(map[string]any)["deep"] = "changed"
	holder.Strings["new"] = "new"
	holder.Native.text = "changed"

	var wg sync.WaitGroup
	results := make([]*parsedHolder, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out := &parsedHolder{}
			if err := s.UnmarshalParsed(parsed, out); err != nil {
				t.Error(err)
				return
			}
			out.Name = "reader"
			out.Ptr.Any.([]any)[0] = "reader"
			out.Any.(map[string]any)["k"].([]any)[0].(map[string]any)["deep"] = "reader"
			out.Strings["reader"] = "reader"
			out.Native.text = "reader"
			results[i] = out
		}(i)
	}
	wg.Wait()

	fromBytes := &parsedHolder{}
	if err = s.Unmarshal(want, fromBytes); err != nil {
		t.Fatal(err)
	}
	again := &parsedHolder{}
	if err = s.UnmarshalParsed(parsed, again); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromBytes, again) {
		t.Fatalf("parsed changed after writer and readers mutated: %#v", again)
	}
	if again.Native.text != "native" || again.Ptr.Any.([]any)[0] != "a" {
		t.Fatalf("unexpected decode: %#v", again)
	}
	if !bytes.Equal(data, want) {
		t.Fatal("bytes changed")
	}
}

func TestUnmarshalParsedMissing(t *testing.T) {
	s := &InternalSerializer{}
	var out parsedHolder
	if err := s.UnmarshalParsed(nil, &out); err == nil {
		t.Fatal("expected an error for a missing parsed value")
	}
	_, parsed, err := s.MarshalParsed(&parsedHolder{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UnmarshalParsed(parsed, out); err == nil {
		t.Fatal("expected an error for a non-pointer destination")
	}
}
