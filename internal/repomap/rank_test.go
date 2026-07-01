package repomap

import (
	"math"
	"sort"
	"testing"
)

// approxEqual reports whether a and b are within tol of each other.
func approxEqual(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func sumScores(scores map[string]float64) float64 {
	var total float64
	for _, v := range scores {
		total += v
	}
	return total
}

func TestPageRankOrderingAndSum(t *testing.T) {
	g := NewGraph()
	g.AddEdge("A", "B", 1)
	g.AddEdge("B", "C", 1)
	g.AddEdge("C", "A", 1)
	g.AddEdge("D", "A", 1)

	scores := g.PageRank(0.85, 30, nil)

	if got := sumScores(scores); !approxEqual(got, 1, 1e-9) {
		t.Fatalf("scores should sum to ~1, got %v", got)
	}

	// A receives edges from C and D, so it must rank highest. D has no
	// in-edges, so it must rank lowest.
	if !(scores["A"] > scores["B"] && scores["A"] > scores["C"]) {
		t.Errorf("expected A to rank highest, got %v", scores)
	}
	if !(scores["D"] < scores["A"] && scores["D"] < scores["B"] && scores["D"] < scores["C"]) {
		t.Errorf("expected D to rank lowest, got %v", scores)
	}

	// Determinism: a second run yields identical results.
	again := g.PageRank(0.85, 30, nil)
	ids := []string{"A", "B", "C", "D"}
	sort.Strings(ids)
	for _, id := range ids {
		if scores[id] != again[id] {
			t.Errorf("non-deterministic result for %s: %v vs %v", id, scores[id], again[id])
		}
	}
}

func TestPageRankSymmetricCycle(t *testing.T) {
	g := NewGraph()
	g.AddEdge("A", "B", 1)
	g.AddEdge("B", "A", 1)

	scores := g.PageRank(0.85, 50, nil)

	if got := sumScores(scores); !approxEqual(got, 1, 1e-9) {
		t.Fatalf("scores should sum to ~1, got %v", got)
	}
	if !approxEqual(scores["A"], 0.5, 1e-9) || !approxEqual(scores["B"], 0.5, 1e-9) {
		t.Errorf("symmetric 2-cycle should give ~0.5 each, got %v", scores)
	}
}

func TestPageRankPersonalizationShiftsMass(t *testing.T) {
	g := NewGraph()
	g.AddEdge("A", "B", 1)
	g.AddEdge("B", "C", 1)
	g.AddEdge("C", "A", 1)
	g.AddEdge("D", "A", 1)

	uniform := g.PageRank(0.85, 50, nil)
	personalized := g.PageRank(0.85, 50, map[string]float64{"D": 1})

	if got := sumScores(personalized); !approxEqual(got, 1, 1e-9) {
		t.Fatalf("personalized scores should sum to ~1, got %v", got)
	}
	if !(personalized["D"] > uniform["D"]) {
		t.Errorf("personalization toward D should raise its score: uniform=%v personalized=%v",
			uniform["D"], personalized["D"])
	}

	// Unknown personalization ids are ignored and fall back to uniform.
	unknown := g.PageRank(0.85, 50, map[string]float64{"Z": 1})
	for id, v := range uniform {
		if !approxEqual(v, unknown[id], 1e-9) {
			t.Errorf("unknown personalization should match uniform for %s: %v vs %v", id, v, unknown[id])
		}
	}
}

func TestPageRankEmptyGraph(t *testing.T) {
	g := NewGraph()
	scores := g.PageRank(0.85, 30, nil)
	if len(scores) != 0 {
		t.Errorf("empty graph should return empty map, got %v", scores)
	}
}

func TestAddEdgeIgnoresNonPositiveWeight(t *testing.T) {
	g := NewGraph()
	g.AddEdge("A", "B", 0)
	g.AddEdge("A", "B", -1)
	if _, ok := g.out["A"]; ok {
		t.Errorf("non-positive weight edges should be ignored, got out-edges for A")
	}
	// Nodes are not registered by an ignored edge.
	if len(g.nodes) != 0 {
		t.Errorf("ignored edge should not register nodes, got %v", g.nodes)
	}
}
