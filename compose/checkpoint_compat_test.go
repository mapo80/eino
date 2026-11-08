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
	"context"
	"math"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudwego/eino/internal/einojson"
	"github.com/cloudwego/eino/schema"
)

// compatSpecialStrings are the strings whose JSON encoding differs between
// encoders: HTML characters, line and paragraph separators, invalid UTF-8,
// escapes, control characters and non-ASCII text.
var compatSpecialStrings = []string{
	"plain", "<a>&b</a>", "line sep ", "bad\xffutf8", "surrogate\xed\xa0\x80",
	"quote\"back\\slash/", "ctrl\x00\x01\x08\t\n\x0b\x0c\r\x1f\x7f", "é€😀", "",
}

// compatCheckpoint is a checkpoint whose state and inputs hold the special
// strings as map keys and as values, and floats at the edges of the range.
func compatCheckpoint() *checkpoint {
	vars := map[string]string{}
	extra := map[string]any{}
	for i, s := range compatSpecialStrings {
		vars[s] = s
		extra[s] = []any{s, float64(i)}
	}
	extra["floats"] = []any{1e21, 1e-7, 123456789.0, math.MaxFloat64, 5e-324, 0.1, -1.5, float64(math.MaxInt64)}
	extra["nested"] = map[string]any{"<k>": map[string]any{" ": []any{nil, true, "x"}}}
	return &checkpoint{
		Inputs: map[string]any{
			"node": &schema.Message{Role: schema.User, Content: "<b> &\xff", Extra: extra},
		},
		State: &deepCopyTestState{Vars: vars, Items: compatSpecialStrings, Nested: map[string][]int{"<n>": {1, 2}},
			Count: math.MaxInt32, Flag: true, Inner: &deepCopyTestInner{Note: "<i> ", Score: 1e-300}},
		SkipPreHandler: map[string]bool{"<node>": true},
		RerunNodes:     []string{"node", "<x>&"},
	}
}

// compatCheckpointFile was written by the byte checkpointer of commit
// 33e49389 (sonic encodes the map keys and decodes the leaves) from
// compatCheckpoint.
const compatCheckpointFile = "testdata/checkpoint_33e49389.json"

// canonicalCheckpoint decodes a checkpoint and encodes it again with
// einojson (map keys sorted, invalid UTF-8 as U+FFFD), with every map key
// that is itself a JSON text (the encoded keys of Go maps) re-encoded the
// same way, so that two encodings of the same checkpoint compare as bytes.
func canonicalCheckpoint(t *testing.T, data []byte) []byte {
	t.Helper()
	var doc any
	require.NoError(t, einojson.Unmarshal(data, &doc))
	out, err := einojson.Marshal(canonicalKeys(doc))
	require.NoError(t, err)
	return out
}

func canonicalKeys(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			var key any
			if einojson.Unmarshal([]byte(k), &key) == nil {
				if b, err := einojson.Marshal(key); err == nil {
					k = string(b)
				}
			}
			out[k] = canonicalKeys(e)
		}
		return out
	case []any:
		for i := range v {
			v[i] = canonicalKeys(v[i])
		}
	}
	return v
}

// A checkpoint written with sonic (map keys encoded and leaves decoded by
// sonic) reads back as the checkpoint written now reads back. The encoding
// written now has the same content as the one written before: the bytes
// differ only in the order of map keys (sonic did not sort them) and in how
// a key with invalid UTF-8, backspace or form feed is spelled (sonic copied
// invalid bytes and wrote \u0008 \u000c; einojson writes U+FFFD, \b, \f).
// Invalid UTF-8 in a map key now reads as U+FFFD, as encoding/json reads it.
func TestCheckpointWrittenWithSonicReadsBack(t *testing.T) {
	ctx := context.Background()
	old, err := os.ReadFile(compatCheckpointFile)
	require.NoError(t, err)

	store := &memoryTreeStore{bytes: map[string][]byte{"old": old}, trees: map[string]any{}}
	cp := newCheckPointer(nil, nil, bytesOnlyStore{store}, nil)
	require.NoError(t, cp.set(ctx, "new", compatCheckpoint()))
	require.Equal(t, string(canonicalCheckpoint(t, old)), string(canonicalCheckpoint(t, store.bytes["new"])))

	fromOld, ok, err := cp.get(ctx, "old")
	require.NoError(t, err)
	require.True(t, ok)
	fromNew, ok, err := cp.get(ctx, "new")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, fromNew, fromOld)

	state := fromOld.State.(*deepCopyTestState)
	for _, s := range compatSpecialStrings {
		// map keys keep their text; invalid UTF-8 reads as U+FFFD
		b, err := einojson.Marshal(s)
		require.NoError(t, err)
		var key string
		require.NoError(t, einojson.Unmarshal(b, &key))
		_, ok := state.Vars[key]
		require.True(t, ok, "key %q", key)
	}
	require.Equal(t, "<i>\u2029", state.Inner.Note)
	require.Equal(t, 1e-300, state.Inner.Score)
	floats := fromOld.Inputs["node"].(*schema.Message).Extra["floats"].([]any)
	require.Equal(t, []any{1e21, 1e-7, 123456789.0, math.MaxFloat64, 5e-324, 0.1, -1.5, float64(math.MaxInt64)}, floats)
}
