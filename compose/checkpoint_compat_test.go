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

	"github.com/bytedance/sonic"
	"github.com/stretchr/testify/require"

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

// sortedJSON re-encodes a JSON document with sorted map keys, keeping
// strings byte for byte (sonic's default configuration does not validate
// UTF-8), so that two encodings of the same checkpoint compare as bytes.
func sortedJSON(t *testing.T, data []byte) []byte {
	t.Helper()
	var doc any
	require.NoError(t, sonic.Unmarshal(data, &doc))
	out, err := sonic.Config{SortMapKeys: true}.Froze().Marshal(doc)
	require.NoError(t, err)
	return out
}

// A checkpoint written before the leaves moved to encoding/json reads back
// as the checkpoint written now reads back, and the encoding written now has
// the same bytes as the one written before, up to the order of map keys
// (sonic does not sort them).
func TestCheckpointWrittenBeforeLeafEncodingChangeReadsBack(t *testing.T) {
	ctx := context.Background()
	old, err := os.ReadFile(compatCheckpointFile)
	require.NoError(t, err)

	store := &memoryTreeStore{bytes: map[string][]byte{"old": old}, trees: map[string]any{}}
	cp := newCheckPointer(nil, nil, bytesOnlyStore{store}, nil)
	require.NoError(t, cp.set(ctx, "new", compatCheckpoint()))
	require.Equal(t, string(sortedJSON(t, old)), string(sortedJSON(t, store.bytes["new"])))

	fromOld, ok, err := cp.get(ctx, "old")
	require.NoError(t, err)
	require.True(t, ok)
	fromNew, ok, err := cp.get(ctx, "new")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, fromNew, fromOld)

	state := fromOld.State.(*deepCopyTestState)
	for _, s := range compatSpecialStrings {
		// map keys keep their bytes, invalid UTF-8 included
		_, ok := state.Vars[s]
		require.True(t, ok, "key %q", s)
	}
	require.Equal(t, "<i> ", state.Inner.Note)
	require.Equal(t, 1e-300, state.Inner.Score)
	floats := fromOld.Inputs["node"].(*schema.Message).Extra["floats"].([]any)
	require.Equal(t, []any{1e21, 1e-7, 123456789.0, math.MaxFloat64, 5e-324, 0.1, -1.5, float64(math.MaxInt64)}, floats)
}
