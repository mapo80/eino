package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloudwego/eino/internal/serialization"
	"github.com/cloudwego/eino/schema"
)

type interruptStateProbe struct {
	A     string
	B     string
	N     int
	Items []string
	Vars  map[string]any
	Inner *testStruct
}

func init() {
	schema.Register[interruptStateProbe]()
}

// canonicalJSON is a checkpoint's JSON with its objects' members in order:
// the serializer writes map members in Go's map order, so two encodings of
// one checkpoint differ only there.
func canonicalJSON(t *testing.T, data []byte) string {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal(data, &v))
	out, err := json.Marshal(v)
	require.NoError(t, err)
	return string(out)
}

// A checkpoint carrying the live state serializes to the document of one
// carrying its deep copy, for states of every shape the codec handles.
func TestCheckpointOfTheLiveStateIsTheCheckpointOfItsCopy(t *testing.T) {
	for name, state := range map[string]any{
		"nil":    nil,
		"struct": &testStruct{A: "x"},
		"rich": &interruptStateProbe{A: "a", B: "a", N: 3, Items: []string{"p", "q"},
			Vars: map[string]any{"n": 1.5, "s": "t", "l": []any{"u", 2.0}, "m": map[string]any{"k": true}}, Inner: &testStruct{A: "in"}},
		"empty": &interruptStateProbe{},
	} {
		t.Run(name, func(t *testing.T) {
			copied, err := deepCopyState(state)
			require.NoError(t, err)
			s := &serialization.InternalSerializer{}
			live, _, err := s.MarshalParsed(&checkpoint{State: state, Inputs: map[string]any{"n": "in"}})
			require.NoError(t, err)
			viaCopy, _, err := s.MarshalParsed(&checkpoint{State: copied, Inputs: map[string]any{"n": "in"}})
			require.NoError(t, err)
			assert.Equal(t, canonicalJSON(t, viaCopy), canonicalJSON(t, live))
		})
	}
}

// An interrupt whose checkpoint the graph serializes itself stores its
// state as it was at the interrupt, reports it, and resumes from it.
func TestInterruptCheckpointCarriesTheLiveState(t *testing.T) {
	store := newInMemoryStore()
	g := NewGraph[string, string](WithGenLocalState(func(ctx context.Context) *interruptStateProbe {
		return &interruptStateProbe{Vars: map[string]any{}}
	}))
	require.NoError(t, g.AddLambdaNode("1", InvokableLambda(func(ctx context.Context, in string) (string, error) {
		return in + "1", ProcessState(ctx, func(_ context.Context, s *interruptStateProbe) error {
			s.A, s.B, s.N, s.Items, s.Vars["k"] = "uno", "uno", 1, []string{"a"}, "v"
			return nil
		})
	})))
	require.NoError(t, g.AddLambdaNode("2", InvokableLambda(func(ctx context.Context, in string) (string, error) {
		var seen string
		err := ProcessState(ctx, func(_ context.Context, s *interruptStateProbe) error {
			seen = fmt.Sprintf("%s/%d/%v/%v", s.A, s.N, s.Items, s.Vars["k"])
			return nil
		})
		return in + "2:" + seen, err
	})))
	require.NoError(t, g.AddEdge(START, "1"))
	require.NoError(t, g.AddEdge("1", "2"))
	require.NoError(t, g.AddEdge("2", END))
	ctx := context.Background()
	r, err := g.Compile(ctx, WithCheckPointStore(store), WithInterruptAfterNodes([]string{"1"}), WithGraphName("root"))
	require.NoError(t, err)
	_, err = r.Invoke(ctx, "s", WithCheckPointID("c"))
	info, ok := ExtractInterruptInfo(err)
	require.True(t, ok)
	want := &interruptStateProbe{A: "uno", B: "uno", N: 1, Items: []string{"a"}, Vars: map[string]any{"k": "v"}}
	assert.Equal(t, want, info.State)
	stored := store.m["c"]
	require.NotEmpty(t, stored)
	var cp checkpoint
	require.NoError(t, (&serialization.InternalSerializer{}).Unmarshal(stored, &cp))
	assert.Equal(t, want, cp.State)
	result, err := r.Invoke(ctx, "s", WithCheckPointID("c"))
	require.NoError(t, err)
	assert.Equal(t, "s12:uno/1/[a]/v", result)
}

type recordingLocker struct {
	mu     sync.Mutex
	held   bool
	locked int
}

func (l *recordingLocker) Lock()   { l.mu.Lock(); l.held = true; l.locked++ }
func (l *recordingLocker) Unlock() { l.held = false; l.mu.Unlock() }

type lockCheckingStore struct {
	inMemoryStore
	t    *testing.T
	lock *recordingLocker
}

func (s *lockCheckingStore) Set(ctx context.Context, id string, data []byte) error {
	if s.lock.held {
		s.t.Error("the store wrote the checkpoint while the state was locked")
	}
	return s.inMemoryStore.Set(ctx, id, data)
}

// setHolding serializes under the state's lock and writes after releasing
// it; the bytes are those of set.
func TestSetHoldingLocksOnlyTheSerialization(t *testing.T) {
	lock := &recordingLocker{}
	store := &lockCheckingStore{inMemoryStore: *newInMemoryStore(), t: t, lock: lock}
	state := &interruptStateProbe{A: "a", B: "a", Vars: map[string]any{"x": 1.0}}
	held := newCheckPointer(nil, nil, store, nil)
	require.NoError(t, held.setHolding(context.Background(), "h", &checkpoint{State: state}, lock))
	plain := newInMemoryStore()
	require.NoError(t, newCheckPointer(nil, nil, plain, nil).set(context.Background(), "p", &checkpoint{State: state}))
	assert.Equal(t, 1, lock.locked)
	assert.Equal(t, canonicalJSON(t, plain.m["p"]), canonicalJSON(t, store.m["h"]))
}

// Nobody changes the live state while it is serialized: a writer that
// takes the state's lock waits, so every stored checkpoint holds a
// consistent state (A and B are always written together). Run with -race.
func TestNoOneChangesTheStateWhileItIsSerialized(t *testing.T) {
	st := &internalState{state: &interruptStateProbe{A: "0", B: "0", Vars: map[string]any{}}}
	store := newInMemoryStore()
	cpr := newCheckPointer(nil, nil, store, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 2000 {
			st.mu.Lock()
			s := st.state.(*interruptStateProbe)
			v := fmt.Sprint(i)
			s.A = v
			s.Vars["v"] = v
			s.B = v
			st.mu.Unlock()
		}
	}()
	for i := range 200 {
		id := fmt.Sprint(i)
		require.NoError(t, cpr.setHolding(context.Background(), id, &checkpoint{State: st.state}, &st.mu))
		var cp checkpoint
		require.NoError(t, (&serialization.InternalSerializer{}).Unmarshal(store.m[id], &cp))
		got := cp.State.(*interruptStateProbe)
		if got.A != got.B || (got.Vars["v"] != nil && got.Vars["v"] != got.A) {
			t.Fatalf("checkpoint %s holds a state changed while serialized: %+v", id, got)
		}
	}
	<-done
}
