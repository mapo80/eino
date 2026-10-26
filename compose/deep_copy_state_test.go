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

package compose

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/cloudwego/eino/internal/serialization"
	"github.com/cloudwego/eino/schema"
)

type deepCopyTestInner struct {
	Note  string
	Score float64
}

type deepCopyTestState struct {
	Vars    map[string]string
	Items   []string
	Nested  map[string][]int
	Count   int
	Flag    bool
	Inner   *deepCopyTestInner
	NilPtr  *deepCopyTestInner
	Empty   map[string]string
	NoItems []string
	hidden  string
}

func init() {
	schema.RegisterName[*deepCopyTestState]("compose_deep_copy_test_state")
	schema.RegisterName[*deepCopyTestInner]("compose_deep_copy_test_inner")
}

// largeDeepCopyState is a state of about size bytes of string values.
func largeDeepCopyState(size int) *deepCopyTestState {
	s := &deepCopyTestState{Vars: map[string]string{}, Nested: map[string][]int{"a": {1, 2, 3}, "b": nil}, Count: 7, Flag: true,
		Inner: &deepCopyTestInner{Note: "inner \"quoted\" è", Score: 1.5}, Empty: map[string]string{}, NoItems: []string{}, hidden: "x"}
	value := strings.Repeat("v", 50<<10)
	for i := 0; i*len(value) < size; i++ {
		s.Vars[fmt.Sprintf("var%03d", i)] = value + fmt.Sprint(i)
		s.Items = append(s.Items, fmt.Sprintf("item-%d", i))
	}
	return s
}

// byteRoundTrip is the copy through the serializer's byte encoding.
func byteRoundTrip(t *testing.T, state any) any {
	t.Helper()
	ser := &serialization.InternalSerializer{}
	data, err := ser.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	out := reflect.New(reflect.TypeOf(state).Elem()).Interface()
	if err := ser.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
	return out
}

// deepCopyState must return what the serializer's byte round trip returns,
// sharing no memory with the source.
func TestDeepCopyStateEqualsSerializerRoundTrip(t *testing.T) {
	for _, state := range []any{largeDeepCopyState(1 << 20), &deepCopyTestState{}, &deepCopyTestInner{Note: "n"}} {
		got, err := deepCopyState(state)
		if err != nil {
			t.Fatal(err)
		}
		want := byteRoundTrip(t, state)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("deepCopyState differs from the serializer round trip:\n got %#v\nwant %#v", got, want)
		}
	}
	src := largeDeepCopyState(1 << 20)
	got, err := deepCopyState(src)
	if err != nil {
		t.Fatal(err)
	}
	cp := got.(*deepCopyTestState)
	cp.Vars["var000"] = "changed"
	cp.Inner.Note = "changed"
	cp.Nested["a"][0] = 99
	if src.Vars["var000"] == "changed" || src.Inner.Note == "changed" || src.Nested["a"][0] == 99 {
		t.Fatal("the copy shares memory with the source")
	}
	if cp.hidden != "" {
		t.Fatal("unexported fields must not be copied, as with the serializer")
	}
}

// totalAlloc is the cumulative bytes allocated on the heap, exact to the
// object (ReadMemStats flushes the per-P caches).
func totalAlloc() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.TotalAlloc
}

// allocatedAfterGC is the bytes f allocates right after a garbage
// collection (the parser's pooled buffers are gone after every GC, as during
// a long run), the least of five runs.
func allocatedAfterGC(f func()) uint64 {
	var best uint64
	for i := 0; i < 5; i++ {
		runtime.GC()
		before := totalAlloc()
		f()
		if n := totalAlloc() - before; i == 0 || n < best {
			best = n
		}
	}
	return best
}

// Copying the state at an interrupt must not encode and parse the whole
// state as one byte document: that adds buffers of the size of the whole
// state on top of the copy itself, so the copy must allocate well below the
// serializer's byte round trip.
func TestDeepCopyStateAllocatesLessThanByteRoundTrip(t *testing.T) {
	const size = 2 << 20
	state := largeDeepCopyState(size)
	copied := allocatedAfterGC(func() {
		if _, err := deepCopyState(state); err != nil {
			t.Fatal(err)
		}
	})
	roundTrip := allocatedAfterGC(func() { byteRoundTrip(t, state) })
	t.Logf("%d-byte state: deepCopyState allocates %d bytes (%.2fx), the byte round trip %d bytes (%.2fx)",
		size, copied, float64(copied)/size, roundTrip, float64(roundTrip)/size)
	if copied > roundTrip/2 {
		t.Fatalf("deepCopyState allocated %d bytes, more than half of the byte round trip (%d bytes)", copied, roundTrip)
	}
}

// A small state must cost about its own size to copy, also right after the
// garbage collections that empty the JSON decoder's pooled parsers: decoding
// a leaf must not allocate a parser with fixed megabyte buffers.
func TestDeepCopyStateOfSmallStateAfterGCAllocatesAboutTheState(t *testing.T) {
	state := &deepCopyTestState{Vars: map[string]string{"a": "1", "b": "2"}, Items: []string{"x", "y"},
		Nested: map[string][]int{"n": {1, 2}}, Count: 3, Inner: &deepCopyTestInner{Note: "note", Score: 0.5}}
	var best uint64
	for i := 0; i < 5; i++ {
		// Two collections: the first moves pooled objects to the victim
		// cache, the second drops them.
		runtime.GC()
		runtime.GC()
		before := totalAlloc()
		if _, err := deepCopyState(state); err != nil {
			t.Fatal(err)
		}
		if n := totalAlloc() - before; i == 0 || n < best {
			best = n
		}
	}
	t.Logf("deepCopyState of a small state after GC allocates %d bytes", best)
	if best > 64<<10 {
		t.Fatalf("deepCopyState of a small state allocated %d bytes after GC (more than 64 KiB)", best)
	}
}
