/*
 * Copyright 2026 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package einojson

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
)

type inner struct {
	N int `json:"n"`
}

type sample struct {
	Name     string            `json:"name"`
	Optional string            `json:"optional,omitempty"`
	Skipped  string            `json:"-"`
	Count    int64             `json:"count"`
	Ratio    float64           `json:"ratio"`
	Flag     bool              `json:"flag"`
	Ptr      *inner            `json:"ptr"`
	PtrOmit  *inner            `json:"ptr_omit,omitempty"`
	List     []string          `json:"list"`
	Ints     []int             `json:"ints,omitempty"`
	Map      map[string]any    `json:"map"`
	Raw      json.RawMessage   `json:"raw,omitempty"`
	Bytes    []byte            `json:"bytes"`
	Any      any               `json:"any"`
	When     time.Time         `json:"when"`
	Str      int               `json:"str,string"`
	Labels   map[string]string `json:"labels,omitempty"`
	inner
}

// marshalCases are values whose bytes must equal sonic's (default
// configuration, map keys sorted for a stable comparison).
func marshalCases() map[string]any {
	return map[string]any{
		"struct": sample{
			Name: "a <b> & \"c\" \\ \u2028\u2029 è 😀 \x01\x1f\t\n\r", Count: math.MaxInt64, Ratio: 0.1,
			Flag: true, Ptr: &inner{N: 1}, List: []string{"x", ""}, Map: map[string]any{"z": 1, "a": []any{nil, true, 1.5}},
			Raw: json.RawMessage(`{"k":[1,2]}`), Bytes: []byte("hi"), Any: map[string]any{"n": nil},
			When: time.Date(2026, 11, 8, 10, 0, 0, 5, time.UTC), Str: 42, Labels: map[string]string{"b": "2", "a": "1"},
			inner: inner{N: 7},
		},
		"zero struct":   sample{},
		"nil":           nil,
		"nil map":       map[string]int(nil),
		"nil slice":     []int(nil),
		"empty slice":   []int{},
		"floats":        []float64{0, -0.0, 1, 1.5, 1e20, 1e21, 1e-6, 1e-7, 123456789.125, math.MaxFloat64, math.SmallestNonzeroFloat64, float64(float32(0.1))},
		"float32":       []float32{0.1, 1e21, 3.4e38},
		"ints":          []any{int8(-1), uint8(255), uint64(math.MaxUint64), int64(math.MinInt64)},
		"string map":    map[string]string{"b": "<x>", "a": "&"},
		"int map":       map[int]string{2: "b", -1: "a"},
		"nested any":    map[string]any{"list": []any{map[string]any{"k": "v"}, "s", 2}, "obj": map[string]any{}},
		"json.Number":   json.Number("12345678901234567890"),
		"tool response": map[string]any{"result": "ok", "items": []string{"a"}, "total": 2},
	}
}

var sortedSonic = sonic.Config{SortMapKeys: true}.Froze()

// TestMarshalMatchesSonic writes each value with einojson and with sonic's
// default configuration (keys sorted): the same bytes.
func TestMarshalMatchesSonic(t *testing.T) {
	for name, v := range marshalCases() {
		got, gotErr := Marshal(v)
		want, wantErr := sortedSonic.Marshal(v)
		if (gotErr == nil) != (wantErr == nil) || string(got) != string(want) {
			t.Errorf("%s:\n got %s (%v)\nwant %s (%v)", name, got, gotErr, want, wantErr)
		}
		gotS, _ := MarshalString(v)
		if gotS != string(got) {
			t.Errorf("%s: MarshalString %s, Marshal %s", name, gotS, got)
		}
	}
}

// TestInvalidUTF8IsReplaced: the one write that differs from sonic. sonic
// copied invalid UTF-8 into the output (which is then not valid JSON);
// einojson writes U+FFFD, as encoding/json does.
func TestInvalidUTF8IsReplaced(t *testing.T) {
	got, err := Marshal(map[string]string{"k\xff": "v\xfe"})
	if err != nil || string(got) != "{\"k\ufffd\":\"v\ufffd\"}" {
		t.Fatalf("got %q, %v", got, err)
	}
	want, _ := sortedSonic.Marshal(map[string]string{"k\xff": "v\xfe"})
	if string(want) != "{\"k\xff\":\"v\xfe\"}" {
		t.Fatalf("sonic now writes %q: update the test", want)
	}
}

var unmarshalInputs = []string{
	`{"name":"x","count":3,"ratio":1.5,"flag":true,"ptr":{"n":2},"list":["a"],"map":{"k":[1,{"z":null}]},"raw":{"r":1},"bytes":"aGk=","any":[1,"s",null,false,{"o":1.25}],"when":"2026-11-08T10:00:00Z","str":"7","n":9}`,
	`{"NAME":"case","Count":1,"FLAG":true}`,
	`{"name":"first","name":"second","count":1,"count":2}`,
	`{"count":12345678901234567890}`,
	`{"count":1.5}`,
	`{"count":"1"}`,
	`{"ratio":1e400}`,
	`{"any":12345678901234567890}`,
	`{"any":1e400}`,
	`{"name":null,"count":null,"ptr":null,"list":null,"map":null}`,
	`{"name":"\u00e8\ud83d\ude00\n\"\\\/","list":["\ud800"]}`,
	`{"unknown":1,"name":"u"}`,
	`{"name":"x"} `,
	`{"name":"x"} trailing`,
	`{"name":"x"}{"name":"y"}`,
	`{"name":}`,
	`{"name":"x",}`,
	`[1,2]`,
	`"string"`,
	`null`,
	``,
	`{"str":"x"}`,
	`{"bytes":"not base64!"}`,
	`{"when":"yesterday"}`,
	`{"ptr":{"n":"1"}}`,
	`{"list":"x"}`,
	`{"map":{"a":1,"a":2}}`,
}

// TestUnmarshalMatchesSonic reads each input with einojson and with sonic
// into a struct, a map and an interface: equal values and equal error
// outcomes.
func TestUnmarshalMatchesSonic(t *testing.T) {
	targets := []func() any{
		func() any { return new(sample) },
		func() any { return new(map[string]any) },
		func() any { return new(any) },
		func() any { return new(map[string]json.RawMessage) },
	}
	for _, in := range unmarshalInputs {
		for _, target := range targets {
			got, want := target(), target()
			gotErr := Unmarshal([]byte(in), got)
			wantErr := sonic.Unmarshal([]byte(in), want)
			name := reflect.TypeOf(got).Elem().String()
			if (gotErr == nil) != (wantErr == nil) {
				if known := knownUnmarshalDifferences[in+" into "+name]; known != "" {
					continue
				}
				t.Errorf("%q into %s: error %v, sonic %v", in, name, gotErr, wantErr)
				continue
			}
			if gotErr == nil && !reflect.DeepEqual(got, want) {
				t.Errorf("%q into %s:\n got %#v\nwant %#v", in, name, got, want)
			}
			gotS := target()
			if errS := UnmarshalString(in, gotS); (errS == nil) != (gotErr == nil) || (errS == nil && !reflect.DeepEqual(gotS, got)) {
				t.Errorf("%q into %s: UnmarshalString differs from Unmarshal", in, name)
			}
		}
	}
}

// knownUnmarshalDifferences are inputs and targets on which the error
// outcome differs from sonic, with the reason.
var knownUnmarshalDifferences = map[string]string{
	`{"ratio":1e400} into map[string]jsontext.Value`: "sonic refused a number beyond float64 even when kept raw; encoding/json keeps it raw",
	`{"any":1e400} into map[string]jsontext.Value`:   "sonic refused a number beyond float64 even when kept raw; encoding/json keeps it raw",
}

// TestKnownUnmarshalDifferencesOccur keeps the list above true.
func TestKnownUnmarshalDifferencesOccur(t *testing.T) {
	for _, in := range []string{`{"ratio":1e400}`, `{"any":1e400}`} {
		var got, want map[string]json.RawMessage
		if err := Unmarshal([]byte(in), &got); err != nil {
			t.Errorf("%s: %v", in, err)
		}
		if err := sonic.Unmarshal([]byte(in), &want); err == nil {
			t.Errorf("%s: sonic now accepts it, remove it from the differences", in)
		}
	}
}

// TestShortEscapes: backspace and form feed are written \b and \f, as
// encoding/json writes them; sonic wrote \u0008 and \u000c. Both read back
// as the same string.
func TestShortEscapes(t *testing.T) {
	got, _ := Marshal("\b\f")
	want, _ := sonic.Marshal("\b\f")
	if string(got) != `"\b\f"` || string(want) != `"\u0008\u000c"` {
		t.Fatalf("got %s, sonic %s", got, want)
	}
	var a, b string
	if Unmarshal(got, &a) != nil || Unmarshal(want, &b) != nil || a != b || a != "\b\f" {
		t.Fatalf("read back %q and %q", a, b)
	}
}

// TestInvalidUTF8ReadsAsReplacement: sonic kept invalid UTF-8 of a string as
// it is; einojson reads U+FFFD, as encoding/json does.
func TestInvalidUTF8ReadsAsReplacement(t *testing.T) {
	var got, want map[string]string
	in := "{\"k\":\"a\xffb\"}"
	if err := Unmarshal([]byte(in), &got); err != nil || got["k"] != "a\ufffdb" {
		t.Fatalf("got %q, %v", got, err)
	}
	if err := sonic.Unmarshal([]byte(in), &want); err != nil || want["k"] != "a\xffb" {
		t.Fatalf("sonic now reads %q, %v: update the test", want, err)
	}
}

// TestValueAtMatchesSonicGet follows key paths with einojson and with sonic's
// GetFromString: the same raw bytes, or an error in both.
func TestValueAtMatchesSonicGet(t *testing.T) {
	docs := []string{
		`{"a":{"b":{"c":[1, 2 ,{"d":"e"}]},"x":"y"},"s":"str","n":null,"dup":1,"dup":2}`,
		`{"data":{"items":[{"id":1}],"total":1}}`,
		`  {"a" : { "b" : "spaced" } }  `,
		`{"a":"\u00e8\n","e\u0073c":true}`,
		`[{"a":1}]`,
		`{"a":1`,
		`{"a":{"b":}}`,
		``,
	}
	paths := []string{"a", "a.b", "a.b.c", "a.x", "s", "n", "dup", "data", "data.items", "data.total", "zz", "a.zz", "s.x", "esc", "a.b.c.d"}
	for _, doc := range docs {
		for _, path := range paths {
			keys := strings.Split(path, ".")
			got, gotErr := ValueAt(doc, keys)
			ikeys := make([]any, len(keys))
			for i, k := range keys {
				ikeys[i] = k
			}
			var want []byte
			node, wantErr := sonic.GetFromString(doc, ikeys...)
			if wantErr == nil {
				want, wantErr = node.MarshalJSON()
			}
			if (gotErr == nil) != (wantErr == nil) || string(got) != string(want) {
				t.Errorf("%q at %s: got %s (%v), sonic %s (%v)", doc, path, got, gotErr, want, wantErr)
			}
		}
	}
}
