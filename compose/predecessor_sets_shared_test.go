/*
 * Copyright 2026 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
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
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// The predecessor sets are computed once per runner and shared read-only;
// channels stay per run.
func TestInitChannelManagerSharesPredecessorSetsNotChannels(t *testing.T) {
	r := &runner{
		chanSubscribeTo:     map[string]*chanCall{},
		successors:          map[string][]string{},
		dataPredecessors:    map[string][]string{END: {"a", "b"}},
		controlPredecessors: map[string][]string{END: {"a", "b", "c"}},
		chanBuilder:         dagChannelBuilder,
		genericHelper:       &genericHelper{},
	}

	first := r.initChannelManager(false)
	second := r.initChannelManager(false)

	require.Equal(t, map[string]map[string]struct{}{END: {"a": {}, "b": {}}}, first.dataPredecessors)
	require.Equal(t, map[string]map[string]struct{}{END: {"a": {}, "b": {}, "c": {}}}, first.controlPredecessors)
	require.Equal(t, reflect.ValueOf(first.dataPredecessors).Pointer(), reflect.ValueOf(second.dataPredecessors).Pointer())
	require.Equal(t, reflect.ValueOf(first.controlPredecessors).Pointer(), reflect.ValueOf(second.controlPredecessors).Pointer())

	require.NotSame(t, first.channels[END], second.channels[END])
	firstEnd := first.channels[END].(*dagChannel)
	secondEnd := second.channels[END].(*dagChannel)
	firstEnd.reportDependencies([]string{"a"})
	require.Equal(t, dependencyStateReady, firstEnd.ControlPredecessors["a"])
	require.Equal(t, dependencyStateWaiting, secondEnd.ControlPredecessors["a"])
}

type isolatedRunState struct {
	visits []string
}

// Two consecutive runs and many concurrent runs of the same compiled DAG must
// keep their channels and their local state isolated.
func TestCompiledGraphRunsKeepStateIsolated(t *testing.T) {
	ctx := context.Background()
	g := NewGraph[map[string]any, map[string]any](WithGenLocalState(func(context.Context) *isolatedRunState {
		return &isolatedRunState{}
	}))
	visit := func(name string) StatePreHandler[map[string]any, *isolatedRunState] {
		return func(_ context.Context, in map[string]any, s *isolatedRunState) (map[string]any, error) {
			s.visits = append(s.visits, name)
			return in, nil
		}
	}
	require.NoError(t, g.AddLambdaNode("a", InvokableLambda(func(_ context.Context, in map[string]any) (map[string]any, error) {
		return map[string]any{"a": fmt.Sprint(in["x"], "-a")}, nil
	}), WithStatePreHandler(visit("a"))))
	require.NoError(t, g.AddLambdaNode("b", InvokableLambda(func(_ context.Context, in map[string]any) (map[string]any, error) {
		return map[string]any{"b": fmt.Sprint(in["x"], "-b")}, nil
	}), WithStatePreHandler(visit("b"))))
	require.NoError(t, g.AddLambdaNode("c", InvokableLambda(func(ctx context.Context, in map[string]any) (map[string]any, error) {
		var visits int
		if err := ProcessState(ctx, func(_ context.Context, s *isolatedRunState) error {
			s.visits = append(s.visits, "c")
			visits = len(s.visits)
			return nil
		}); err != nil {
			return nil, err
		}
		return map[string]any{"out": fmt.Sprint(in["a"], "+", in["b"]), "visits": visits}, nil
	})))
	require.NoError(t, g.AddEdge(START, "a"))
	require.NoError(t, g.AddEdge(START, "b"))
	require.NoError(t, g.AddEdge("a", "c"))
	require.NoError(t, g.AddEdge("b", "c"))
	require.NoError(t, g.AddEdge("c", END))

	r, err := g.Compile(ctx, WithNodeTriggerMode(AllPredecessor))
	require.NoError(t, err)

	check := func(input string) error {
		out, err := r.Invoke(ctx, map[string]any{"x": input})
		if err != nil {
			return err
		}
		want := map[string]any{"out": input + "-a+" + input + "-b", "visits": 3}
		if !reflect.DeepEqual(want, out) {
			return fmt.Errorf("input %s: got %v, want %v", input, out, want)
		}
		return nil
	}

	require.NoError(t, check("first"))
	require.NoError(t, check("second"))

	const runs = 32
	var wg sync.WaitGroup
	errs := make(chan error, runs)
	for i := 0; i < runs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- check(fmt.Sprintf("run%d", i))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}
