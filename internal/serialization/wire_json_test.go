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

package serialization

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/internal/einojson"
)

// wireMirror and typeMirror have the fields and tags of internalStruct and
// valueType without their JSON methods: einojson reads and writes them by
// reflection, the reference of the token codec.
type wireMirror struct {
	Type        *typeMirror            `json:",omitempty"`
	JSONValue   json.RawMessage        `json:",omitempty"`
	MapValues   map[string]*wireMirror `json:",omitempty"`
	SliceValues []*wireMirror          `json:",omitempty"`
}

type typeMirror struct {
	PointerNum     uint32      `json:",omitempty"`
	SimpleType     string      `json:",omitempty"`
	StructType     string      `json:",omitempty"`
	MapKeyType     *typeMirror `json:",omitempty"`
	MapValueType   *typeMirror `json:",omitempty"`
	SliceValueType *typeMirror `json:",omitempty"`
}

func mirrorOf(is *internalStruct) *wireMirror {
	if is == nil {
		return nil
	}
	m := &wireMirror{Type: typeMirrorOf(is.Type), JSONValue: is.JSONValue}
	if is.MapValues != nil {
		m.MapValues = map[string]*wireMirror{}
		for k, v := range is.MapValues {
			m.MapValues[k] = mirrorOf(v)
		}
	}
	if is.SliceValues != nil {
		m.SliceValues = []*wireMirror{}
		for _, v := range is.SliceValues {
			m.SliceValues = append(m.SliceValues, mirrorOf(v))
		}
	}
	return m
}

func typeMirrorOf(vt *valueType) *typeMirror {
	if vt == nil {
		return nil
	}
	return &typeMirror{PointerNum: vt.PointerNum, SimpleType: vt.SimpleType, StructType: vt.StructType,
		MapKeyType: typeMirrorOf(vt.MapKeyType), MapValueType: typeMirrorOf(vt.MapValueType), SliceValueType: typeMirrorOf(vt.SliceValueType)}
}

type wireSample struct {
	Name  string
	Items []any
	Vars  map[string]string
	Inner *wireSampleInner
	Nil   *wireSampleInner
	N     int
	F     float64
}

type wireSampleInner struct {
	Note  string
	Score float64
}

func init() {
	_ = GenericRegister[wireSample]("wire_sample")
	_ = GenericRegister[wireSampleInner]("wire_sample_inner")
}

// wireDocuments are checkpoint wires: written by the serializer from values
// with nested structs, maps, slices, nil pointers and special strings, the
// checkpoint written with sonic, and hand-made documents with case variants,
// unknown members, nulls, repeated members and whitespace in raw values.
func wireDocuments(t *testing.T) [][]byte {
	t.Helper()
	s := &InternalSerializer{}
	var docs [][]byte
	for _, v := range []any{
		&wireSample{Name: "<a>&b \u2028", Items: []any{"x", 1.5, nil, true, map[string]any{"k": []any{}}}, Vars: map[string]string{"\x00ctrl\t": "v", "è😀": ""},
			Inner: &wireSampleInner{Note: "n", Score: math.MaxFloat64}, N: math.MaxInt32, F: 5e-324},
		map[string]any{"one": 1.0},
		[]any{nil, []any{nil}},
		"plain",
		nil,
	} {
		b, err := s.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, b)
	}
	old, err := os.ReadFile("../../compose/testdata/checkpoint_33e49389.json")
	if err != nil {
		t.Fatal(err)
	}
	docs = append(docs, old)
	for _, d := range []string{
		`{"type":{"pointernum":2,"simpletype":"_eino_string"},"jsonvalue":"x"}`,
		`{"Type":null,"JSONValue":null,"MapValues":null,"SliceValues":null}`,
		`{"JSONValue":{ "a" : [ 1 , 2 ] },"Unknown":{"x":[1,{"y":null}]},"SliceValues":[null,{},{"JSONValue":1}]}`,
		`{"JSONValue":1,"JSONValue":2,"MapValues":{"\"k\"":{"JSONValue":"a"},"\"k\"":{"JSONValue":"b"}}}`,
		`{"Type":{"MapKeyType":{"SimpleType":"_eino_string"},"MapValueType":null,"SliceValueType":{"PointerNum":0}},"MapValues":{}}`,
		`{"SliceValues":[]}`,
		`{"T\u0079pe":{"SimpleType":"\u005feino_int"}}`,
		`{}`,
		`null`,
	} {
		docs = append(docs, []byte(d))
	}
	return docs
}

// TestWireReadsAsReflection: every wire document read by the token codec
// gives the tree einojson gives by reflection.
func TestWireReadsAsReflection(t *testing.T) {
	for _, d := range wireDocuments(t) {
		got := &internalStruct{}
		gotErr := einojson.Unmarshal(d, got)
		want := &wireMirror{}
		wantErr := einojson.Unmarshal(d, want)
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("%s: error %v, reflection %v", d, gotErr, wantErr)
		}
		if gotErr == nil && !reflect.DeepEqual(mirrorOf(got), want) {
			g, _ := json.Marshal(mirrorOf(got))
			w, _ := json.Marshal(want)
			t.Fatalf("%s:\n got %s\nwant %s", d, g, w)
		}
	}
}

// TestWireWritesAsReflection: every tree written by the token codec reads
// back as the tree written by reflection reads back (raw values compacted
// by both), and has the bytes einojson writes by reflection when no
// map has more than one member (the token codec writes map members in map
// order, as sonic did; reflection sorts them).
func TestWireWritesAsReflection(t *testing.T) {
	exact := 0
	for _, d := range wireDocuments(t) {
		tree := &internalStruct{}
		if err := einojson.Unmarshal(d, tree); err != nil {
			continue
		}
		got, err := einojson.Marshal(tree)
		if err != nil {
			t.Fatal(err)
		}
		want, err := einojson.Marshal(mirrorOf(tree))
		if err != nil {
			t.Fatal(err)
		}
		back, wantBack := &wireMirror{}, &wireMirror{}
		if err := einojson.Unmarshal(got, back); err != nil {
			t.Fatal(err)
		}
		if err := einojson.Unmarshal(want, wantBack); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(back, wantBack) {
			t.Fatalf("%s: written %s reads back differently from %s", d, got, want)
		}
		if maxMapLen(tree) <= 1 {
			exact++
			if string(got) != string(want) {
				t.Fatalf("%s:\n got %s\nwant %s", d, got, want)
			}
		}
	}
	if exact < 8 {
		t.Fatalf("only %d documents compared byte for byte", exact)
	}
}

func maxMapLen(is *internalStruct) int {
	if is == nil {
		return 0
	}
	n := len(is.MapValues)
	for _, v := range is.MapValues {
		n = max(n, maxMapLen(v))
	}
	for _, v := range is.SliceValues {
		n = max(n, maxMapLen(v))
	}
	return n
}

// TestWireRefusesWhatReflectionRefuses: wrong types are errors in both.
func TestWireRefusesWhatReflectionRefuses(t *testing.T) {
	for _, d := range []string{
		`[]`, `"x"`, `1`, `{"Type":"x"}`, `{"Type":{"PointerNum":-1}}`, `{"Type":{"PointerNum":1.5}}`,
		`{"Type":{"PointerNum":4294967296}}`, `{"Type":{"SimpleType":1}}`, `{"MapValues":[]}`, `{"SliceValues":{}}`,
		`{"SliceValues":[1]}`, `{"MapValues":{"k":"v"}}`, `{"JSONValue":}`, `{"Type":{}`,
	} {
		gotErr := einojson.Unmarshal([]byte(d), &internalStruct{})
		wantErr := einojson.Unmarshal([]byte(d), &wireMirror{})
		if (gotErr == nil) != (wantErr == nil) {
			t.Errorf("%s: error %v, reflection %v", d, gotErr, wantErr)
		}
	}
}
