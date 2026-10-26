/*
 * Copyright 2025 CloudWeGo Authors
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
	"math/rand"
	"reflect"
	"testing"

	"github.com/bytedance/sonic"
)

func leafCompatStrings() []string {
	out := []string{"", "plain", "<a>&b</a>", "line sep ", "bad\xffutf8", "\xc3", "surrogate\xed\xa0\x80",
		"quote\"back\\slash/", "ctrl\x00\x01\x08\t\n\x0b\x0c\r\x1f\x7f", "é€😀", "\U0010ffff"}
	for c := 0; c < 256; c++ {
		out = append(out, string([]byte{byte(c)}), "a"+string([]byte{byte(c)})+"z")
	}
	r := rand.New(rand.NewSource(1))
	alphabet := []byte("ab<>&\"\\/\x00\x1f\x7f\xe2\x80\xa8\xff\xc3\xa9 ")
	for i := 0; i < 5000; i++ {
		b := make([]byte, r.Intn(12))
		for j := range b {
			if r.Intn(4) == 0 {
				b[j] = byte(r.Intn(256))
			} else {
				b[j] = alphabet[r.Intn(len(alphabet))]
			}
		}
		out = append(out, string(b))
	}
	return out
}

// A plain string map key is encoded with the bytes sonic.MarshalString
// writes, so checkpoints keep the same bytes.
func TestMapKeyEncodingEqualsSonic(t *testing.T) {
	for _, s := range leafCompatStrings() {
		want, err := sonic.MarshalString(s)
		if err != nil {
			t.Fatal(err)
		}
		got, err := marshalMapKey(s)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("key %q: got %q, sonic %q", s, got, want)
		}
	}
	for _, k := range []any{1.5, 1e21, 1e-7, math.MaxFloat64, 5e-324, -0.0, 7, int64(math.MaxInt64), uint64(math.MaxUint64), true} {
		want, _ := sonic.MarshalString(k)
		got, err := marshalMapKey(k)
		if err != nil || got != want {
			t.Fatalf("key %v: got %q (%v), sonic %q", k, got, err, want)
		}
	}
}

type leafCompatStruct struct {
	Name  string
	N     int
	F     float64
	Any   any
	M     map[string]any
	inner string
}

// Leaves decode to the value sonic decodes, from the JSON encoding/json
// writes for leaves and from the JSON sonic writes for map keys, invalid
// UTF-8 included.
func TestLeafDecodingEqualsSonic(t *testing.T) {
	var docs [][]byte
	for _, s := range leafCompatStrings() {
		j, _ := json.Marshal(s)
		k, _ := sonic.MarshalString(s)
		docs = append(docs, j, []byte(k))
	}
	for _, v := range []any{1e21, 1e-7, 123456789.0, math.MaxFloat64, 5e-324, 0.1, -1.5, float64(math.MaxInt64), int64(math.MaxInt64), uint64(math.MaxUint64), true, nil,
		map[string]any{"<k>": []any{1.0, "x ", nil}, "b\xff": 2.0},
		leafCompatStruct{Name: "<n>&", N: -3, F: 1e-300, Any: []any{"a", 1.0}, M: map[string]any{" ": true}}} {
		j, _ := json.Marshal(v)
		docs = append(docs, j)
	}
	docs = append(docs, []byte(`{"name":"lower","NAME":"upper","n":7}`), []byte(`"\ud800x"`), []byte(`"\u0000"`), []byte(`{"Name":"a","Name":"b"}`))
	targets := []reflect.Type{reflect.TypeOf(""), reflect.TypeOf(0.0), reflect.TypeOf(int64(0)), reflect.TypeOf(uint64(0)), reflect.TypeOf(true),
		reflect.TypeOf((*any)(nil)).Elem(), reflect.TypeOf(map[string]any{}), reflect.TypeOf([]any{}), reflect.TypeOf(leafCompatStruct{}), reflect.TypeOf(&leafCompatStruct{})}
	for _, d := range docs {
		for _, typ := range targets {
			want := reflect.New(typ)
			wantErr := sonic.Unmarshal(d, want.Interface())
			got := reflect.New(typ)
			gotErr := unmarshalLeaf(d, got.Interface())
			if (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("%s into %v: error %v, sonic error %v", d, typ, gotErr, wantErr)
			}
			if wantErr == nil && !reflect.DeepEqual(got.Elem().Interface(), want.Elem().Interface()) {
				t.Fatalf("%s into %v: got %#v, sonic %#v", d, typ, got.Elem().Interface(), want.Elem().Interface())
			}
		}
	}
}
