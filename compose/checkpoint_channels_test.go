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

package compose

import (
	"bytes"
	"context"
	"reflect"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A checkpoint names only the channels that left the state they were built
// in; restoring it gives the same channels as restoring every channel.
func TestCheckpointChannelsRestoreAsEveryChannel(t *testing.T) {
	built := func(trigger NodeTriggerMode) map[string]channel {
		builder := pregelChannelBuilder
		if trigger == AllPredecessor {
			builder = dagChannelBuilder
		}
		return map[string]channel{
			"a": builder([]string{"start"}, []string{"start"}, nil, nil),
			"b": builder([]string{"a"}, []string{"a"}, nil, nil),
			"c": builder([]string{"a", "b"}, []string{"b"}, nil, nil),
		}
	}
	for _, trigger := range []NodeTriggerMode{AnyPredecessor, AllPredecessor} {
		live := built(trigger)
		assert.NoError(t, live["b"].reportValues(map[string]any{"a": "from-a"}))
		live["c"].reportDependencies([]string{"a"})
		kept := checkpointChannels(live)
		for _, restoredFrom := range []map[string]channel{live, kept} {
			fresh := built(trigger)
			cm := &channelManager{channels: fresh}
			assert.NoError(t, cm.loadChannels(restoredFrom))
			if trigger == AnyPredecessor {
				assert.Equal(t, map[string]any{"a": "from-a"}, fresh["b"].(*pregelChannel).Values)
			}
			full := &channelManager{channels: built(trigger)}
			assert.NoError(t, full.loadChannels(live))
			for key := range live {
				assert.True(t, reflect.DeepEqual(full.channels[key], fresh[key]), "%v channel %s", trigger, key)
			}
		}
		_, keptA := kept["a"]
		assert.False(t, keptA, "%v: a channel as built was kept", trigger)
		assert.Contains(t, kept, "b")
		if trigger == AllPredecessor {
			assert.Contains(t, kept, "c", "a channel with a reported dependency was dropped")
		}
	}
}

// A graph of many nodes paused at one: the checkpoint names only the
// channels in use, and the resumed run answers as the full graph would.
func TestCheckpointOmitsChannelsAsBuilt(t *testing.T) {
	store := newInMemoryStore()
	g := NewGraph[string, string](WithGenLocalState(func(ctx context.Context) (state *testStruct) {
		return &testStruct{A: ""}
	}))
	const n = 50
	for i := 0; i < n; i++ {
		i := i
		assert.NoError(t, g.AddLambdaNode("n"+strconv.Itoa(i), InvokableLambda(func(ctx context.Context, input string) (string, error) {
			return input + "." + strconv.Itoa(i), nil
		})))
	}
	assert.NoError(t, g.AddEdge(START, "n0"))
	for i := 0; i+1 < n; i++ {
		assert.NoError(t, g.AddEdge("n"+strconv.Itoa(i), "n"+strconv.Itoa(i+1)))
	}
	assert.NoError(t, g.AddEdge("n"+strconv.Itoa(n-1), END))
	ctx := context.Background()
	r, err := g.Compile(ctx, WithCheckPointStore(store), WithInterruptBeforeNodes([]string{"n3"}), WithGraphName("root"))
	assert.NoError(t, err)
	_, err = r.Invoke(ctx, "start", WithCheckPointID("many"))
	info, ok := ExtractInterruptInfo(err)
	assert.True(t, ok)
	saved := store.m["many"]
	// A channel key is written as a quoted JSON string inside the map's key.
	assert.True(t, bytes.Contains(saved, []byte(`\"n3\"`)), "checkpoint lost the channel in use")
	for i := 5; i < n; i++ {
		assert.False(t, bytes.Contains(saved, []byte(`\"n`+strconv.Itoa(i)+`\"`)), "checkpoint names unused channel n%d", i)
	}
	result, err := r.Invoke(ResumeWithData(ctx, info.InterruptContexts[0].ID, &testStruct{A: "state"}), "start", WithCheckPointID("many"))
	assert.NoError(t, err)
	want := "start"
	for i := 0; i < n; i++ {
		want += "." + strconv.Itoa(i)
	}
	assert.Equal(t, want, result)
}
