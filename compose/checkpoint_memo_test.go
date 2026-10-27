package compose

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloudwego/eino/internal/serialization"
	"github.com/cloudwego/eino/schema"
)

// countByteDecodes counts the checkpoints decoded from store bytes until the
// test ends.
func countByteDecodes(t *testing.T) *int64 {
	var n int64
	prev := unmarshalCheckpointBytes
	unmarshalCheckpointBytes = func(s Serializer, data []byte, cp *checkpoint) error {
		atomic.AddInt64(&n, 1)
		return prev(s, data, cp)
	}
	t.Cleanup(func() { unmarshalCheckpointBytes = prev })
	return &n
}

// copyingStore keeps its own copy of the bytes, like an external store.
type copyingStore struct {
	mu     sync.Mutex
	m      map[string][]byte
	setErr error
}

func newCopyingStore() *copyingStore { return &copyingStore{m: map[string][]byte{}} }

func (s *copyingStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[id]
	return append([]byte(nil), v...), ok, nil
}

func (s *copyingStore) Set(_ context.Context, id string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	s.m[id] = append([]byte(nil), data...)
	return nil
}

func (s *copyingStore) put(id string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = append([]byte(nil), data...)
}

// decodeStored decodes what the store holds, as a reader without memory would.
func decodeStored(t *testing.T, store CheckPointStore, id string) *checkpoint {
	data, ok, err := store.Get(context.Background(), id)
	require.NoError(t, err)
	require.True(t, ok)
	cp := &checkpoint{}
	require.NoError(t, (&serialization.InternalSerializer{}).Unmarshal(data, cp))
	return cp
}

func memoTestCheckpoint(marker string) *checkpoint {
	cp := treeTestCheckpoint()
	cp.Inputs["marker"] = marker
	cp.SubGraphs = map[string]*checkpoint{"sub": treeTestCheckpoint()}
	return cp
}

// A read of the bytes just written decodes the checkpoint without parsing
// the bytes again, and gives what parsing them gives.
func TestCheckPointGetAfterSetSkipsByteDecode(t *testing.T) {
	ctx := context.Background()
	decodes := countByteDecodes(t)
	store := newCopyingStore()
	cpr := newCheckPointer(nil, nil, store, nil)

	require.NoError(t, cpr.set(ctx, "memo-get-after-set", memoTestCheckpoint("original")))
	want := decodeStored(t, store, "memo-get-after-set")
	for i := 0; i < 2; i++ {
		got, ok, err := cpr.get(ctx, "memo-get-after-set")
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, want, got)
	}
	assert.Equal(t, int64(0), atomic.LoadInt64(decodes))
}

// The reader owns the checkpoint it gets: what the writer changes after the
// write and what a reader changes are seen by no other reader.
func TestCheckPointMemoIsolatedFromWriterAndReaders(t *testing.T) {
	ctx := context.Background()
	store := newCopyingStore()
	cpr := newCheckPointer(nil, nil, store, nil)
	written := memoTestCheckpoint("original")
	require.NoError(t, cpr.set(ctx, "memo-isolation", written))
	want := decodeStored(t, store, "memo-isolation")

	msg := written.Inputs["node"].(*schema.Message)
	msg.Content = "writer"
	msg.Extra["map"].(map[string]any)["k"] = "writer"
	written.RerunNodes[0] = "writer"
	written.SubGraphs["sub"].Inputs["node"].(*schema.Message).Content = "writer"
	delete(written.SubGraphs, "sub")

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, ok, err := cpr.get(ctx, "memo-isolation")
			if !assert.NoError(t, err) || !assert.True(t, ok) {
				return
			}
			assert.Equal(t, want, got)
			// what a resumed graph does with its checkpoint
			sub := got.SubGraphs["sub"]
			delete(got.SubGraphs, "sub")
			sub.Inputs["node"].(*schema.Message).Content = "reader"
			got.Inputs["node"].(*schema.Message).Extra["list"].([]any)[1].(map[string]any)["deep"] = "reader"
			got.Inputs["marker"] = "reader"
			got.RerunNodes[0] = "reader"
		}()
	}
	wg.Wait()

	got, ok, err := cpr.get(ctx, "memo-isolation")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, want, got)
	require.Contains(t, got.SubGraphs, "sub")
	require.Equal(t, "a", got.SubGraphs["sub"].Inputs["node"].(*schema.Message).Content)
}

// Bytes in the store that are not the bytes this process wrote are decoded:
// written by another process, changed by the store after the write, or left
// in place by a failed write.
func TestCheckPointMemoFollowsTheStore(t *testing.T) {
	ctx := context.Background()
	s := &serialization.InternalSerializer{}

	t.Run("external writer", func(t *testing.T) {
		decodes := countByteDecodes(t)
		store := newCopyingStore()
		cpr := newCheckPointer(nil, nil, store, nil)
		require.NoError(t, cpr.set(ctx, "memo-external", memoTestCheckpoint("original")))
		other, err := s.Marshal(memoTestCheckpoint("external"))
		require.NoError(t, err)
		store.put("memo-external", other)

		got, ok, err := cpr.get(ctx, "memo-external")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "external", got.Inputs["marker"])
		assert.Equal(t, decodeStored(t, store, "memo-external"), got)
		assert.Equal(t, int64(1), atomic.LoadInt64(decodes))
	})

	t.Run("store changes the bytes it keeps", func(t *testing.T) {
		store := newInMemoryStore()
		cpr := newCheckPointer(nil, nil, store, nil)
		require.NoError(t, cpr.set(ctx, "memo-in-place", memoTestCheckpoint("original")))
		kept := store.m["memo-in-place"]
		at := bytes.Index(kept, []byte("original"))
		require.GreaterOrEqual(t, at, 0)
		copy(kept[at:], "modified")

		got, ok, err := cpr.get(ctx, "memo-in-place")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "modified", got.Inputs["marker"])
	})

	t.Run("failed write", func(t *testing.T) {
		store := newCopyingStore()
		cpr := newCheckPointer(nil, nil, store, nil)
		require.NoError(t, cpr.set(ctx, "memo-failed", memoTestCheckpoint("original")))
		store.setErr = errors.New("store unavailable")
		require.Error(t, cpr.set(ctx, "memo-failed", memoTestCheckpoint("lost")))

		got, ok, err := cpr.get(ctx, "memo-failed")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "original", got.Inputs["marker"])
	})

	t.Run("new process reads the persisted bytes", func(t *testing.T) {
		store := newCopyingStore()
		writer := newCheckPointer(nil, nil, store, nil)
		require.NoError(t, writer.set(ctx, "memo-persisted", memoTestCheckpoint("original")))
		persisted, _, err := store.Get(ctx, "memo-persisted")
		require.NoError(t, err)

		// same bytes in another store, read by another graph
		other := newCopyingStore()
		other.put("memo-persisted", persisted)
		reader := newCheckPointer(nil, nil, other, nil)
		got, ok, err := reader.get(ctx, "memo-persisted")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, decodeStored(t, other, "memo-persisted"), got)
		stored, _, err := other.Get(ctx, "memo-persisted")
		require.NoError(t, err)
		assert.Equal(t, persisted, stored)
	})
}

// A resumed subgraph takes its checkpoint out of the one read from the store:
// resuming twice from the same checkpoint resumes the subgraph twice.
func TestCheckPointMemoSubGraphResumedTwice(t *testing.T) {
	decodes := countByteDecodes(t)
	sub := NewGraph[map[string]any, map[string]any]()
	require.NoError(t, sub.AddLambdaNode("s1", InvokableLambda(func(ctx context.Context, in map[string]any) (map[string]any, error) {
		in["s1"] = true
		return in, nil
	})))
	require.NoError(t, sub.AddLambdaNode("s2", InvokableLambda(func(ctx context.Context, in map[string]any) (map[string]any, error) {
		in["s2"] = len(in)
		in["s1"] = "changed by s2"
		return in, nil
	})))
	require.NoError(t, sub.AddEdge(START, "s1"))
	require.NoError(t, sub.AddEdge("s1", "s2"))
	require.NoError(t, sub.AddEdge("s2", END))
	g := NewGraph[map[string]any, map[string]any]()
	require.NoError(t, g.AddGraphNode("sub", sub, WithGraphCompileOptions(WithInterruptAfterNodes([]string{"s1"}))))
	require.NoError(t, g.AddEdge(START, "sub"))
	require.NoError(t, g.AddEdge("sub", END))

	ctx := context.Background()
	r, err := g.Compile(ctx, WithCheckPointStore(newCopyingStore()), WithGraphName("root"))
	require.NoError(t, err)
	_, err = r.Invoke(ctx, map[string]any{"in": "x"}, WithCheckPointID("memo-sub"))
	_, ok := ExtractInterruptInfo(err)
	require.True(t, ok, "%v", err)

	for i := 0; i < 2; i++ {
		out, err := r.Invoke(ctx, nil, WithCheckPointID("memo-sub"), WithWriteToCheckPointID("memo-sub-next"))
		require.NoError(t, err, "resume %d", i)
		assert.Equal(t, map[string]any{"in": "x", "s1": "changed by s2", "s2": 2}, out, "resume %d", i)
	}
	assert.Equal(t, int64(0), atomic.LoadInt64(decodes))
}

// A task canceled by an interrupt timeout keeps running and may change its
// input after the checkpoint is written: the rerun reads the input of the
// checkpoint.
func TestCheckPointMemoCanceledTaskKeepsRunning(t *testing.T) {
	decodes := countByteDecodes(t)
	release := make(chan struct{})
	finished := make(chan struct{})
	var calls, seenOnRerun int64
	var rerunValue string
	g := NewGraph[*testStruct, string]()
	require.NoError(t, g.AddLambdaNode("1", InvokableLambda(func(ctx context.Context, in *testStruct) (string, error) {
		if atomic.AddInt64(&calls, 1) == 1 {
			defer close(finished)
			<-release
			in.A = "late"
			return in.A, nil
		}
		atomic.AddInt64(&seenOnRerun, 1)
		rerunValue = in.A
		return in.A, nil
	})))
	require.NoError(t, g.AddEdge(START, "1"))
	require.NoError(t, g.AddEdge("1", END))
	r, err := g.Compile(context.Background(), WithCheckPointStore(newCopyingStore()))
	require.NoError(t, err)

	ctx, interrupt := WithGraphInterrupt(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		interrupt(WithGraphInterruptTimeout(0))
	}()
	_, err = r.Invoke(ctx, &testStruct{A: "initial"}, WithCheckPointID("memo-canceled"))
	info, ok := ExtractInterruptInfo(err)
	require.True(t, ok, "%v", err)
	require.Equal(t, []string{"1"}, info.RerunNodes)

	close(release)
	<-finished

	out, err := r.Invoke(context.Background(), nil, WithCheckPointID("memo-canceled"))
	require.NoError(t, err)
	assert.Equal(t, "initial", out)
	assert.Equal(t, int64(1), atomic.LoadInt64(&seenOnRerun))
	assert.Equal(t, "initial", rerunValue)
	assert.Equal(t, int64(0), atomic.LoadInt64(decodes))
}

// plainSerializer is the default serializer behind another type, so the
// checkpointer cannot recognize it and always decodes the bytes.
type plainSerializer struct {
	serialization.InternalSerializer
}

// Interrupting and resuming a graph with state several times gives the same
// interrupts and the same result with and without decoding from memory.
func TestCheckPointMemoInterruptResumeSameAsBytes(t *testing.T) {
	run := func(t *testing.T, serializer Serializer) (infos []*InterruptInfo, result string) {
		g := NewGraph[string, string](WithGenLocalState(func(ctx context.Context) *testStruct { return &testStruct{A: "s"} }))
		for _, key := range []string{"1", "2", "3"} {
			key := key
			require.NoError(t, g.AddLambdaNode(key, InvokableLambda(func(ctx context.Context, in string) (string, error) {
				return in + key, nil
			}), WithStatePreHandler(func(ctx context.Context, in string, s *testStruct) (string, error) {
				s.A += key
				return in + s.A, nil
			})))
		}
		require.NoError(t, g.AddEdge(START, "1"))
		require.NoError(t, g.AddEdge("1", "2"))
		require.NoError(t, g.AddEdge("2", "3"))
		require.NoError(t, g.AddEdge("3", END))
		opts := []GraphCompileOption{WithCheckPointStore(newCopyingStore()), WithInterruptAfterNodes([]string{"1", "2"})}
		if serializer != nil {
			opts = append(opts, WithSerializer(serializer))
		}
		r, err := g.Compile(context.Background(), opts...)
		require.NoError(t, err)
		input := "in"
		for {
			out, err := r.Invoke(context.Background(), input, WithCheckPointID("memo-interrupt"))
			if err == nil {
				return infos, out
			}
			info, ok := ExtractInterruptInfo(err)
			require.True(t, ok, "%v", err)
			info.InterruptContexts = nil
			infos = append(infos, info)
			input = ""
		}
	}

	decodes := countByteDecodes(t)
	memoInfos, memoResult := run(t, nil)
	assert.Equal(t, int64(0), atomic.LoadInt64(decodes))
	plainInfos, plainResult := run(t, &plainSerializer{})
	assert.Equal(t, int64(2), atomic.LoadInt64(decodes))
	assert.Len(t, memoInfos, 2)
	assert.Equal(t, plainInfos, memoInfos)
	assert.Equal(t, plainResult, memoResult)
}
