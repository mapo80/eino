package compose

import (
	"context"
	"fmt"
	"testing"
)

func chainOfTypedLambdas(tb testing.TB, n int) *Graph[string, string] {
	tb.Helper()
	g := NewGraph[string, string]()
	id := func(i int) string { return fmt.Sprintf("n%d", i) }
	for i := 0; i < n; i++ {
		if err := g.AddLambdaNode(id(i), InvokableLambda(func(_ context.Context, s string) (string, error) { return s, nil })); err != nil {
			tb.Fatal(err)
		}
	}
	if err := g.AddEdge(START, id(0)); err != nil {
		tb.Fatal(err)
	}
	for i := 1; i < n; i++ {
		if err := g.AddEdge(id(i-1), id(i)); err != nil {
			tb.Fatal(err)
		}
	}
	if err := g.AddEdge(id(n-1), END); err != nil {
		tb.Fatal(err)
	}
	return g
}

// Every edge between typed nodes is validated when it is added, so nothing is
// left to validate. Keeping the emptied keys makes each later AddEdge rescan
// all of them: the cost of building a graph grows with the square of its edges.
func TestToValidateMapKeepsNoResolvedKeys(t *testing.T) {
	g := chainOfTypedLambdas(t, 500)
	if left := len(g.graph.toValidateMap); left != 0 {
		t.Fatalf("toValidateMap keeps %d resolved start nodes (each AddEdge rescans them)", left)
	}
	if _, err := g.Compile(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkChainOfTypedLambdas(b *testing.B) {
	for _, n := range []int{1000, 2000, 4000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				chainOfTypedLambdas(b, n)
			}
		})
	}
}
