package repomap

import (
	"math"
	"sort"
)

// Graph is a directed, weighted graph of string-identified nodes (file paths)
// used to rank files by PageRank over their reference edges.
type Graph struct {
	// nodes is the set of registered node ids.
	nodes map[string]struct{}
	// out maps a node to its out-edges (target -> accumulated weight).
	out map[string]map[string]float64
	// outWeight maps a node to the sum of its out-edge weights.
	outWeight map[string]float64
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{
		nodes:     make(map[string]struct{}),
		out:       make(map[string]map[string]float64),
		outWeight: make(map[string]float64),
	}
}

// AddNode registers a node id (no-op if it already exists).
func (g *Graph) AddNode(id string) {
	if _, ok := g.nodes[id]; ok {
		return
	}
	g.nodes[id] = struct{}{}
}

// AddEdge adds weight to the directed edge from->to, registering both nodes if
// new. Repeated edges accumulate weight. A non-positive weight is ignored.
func (g *Graph) AddEdge(from, to string, weight float64) {
	if weight <= 0 {
		return
	}
	g.AddNode(from)
	g.AddNode(to)
	edges, ok := g.out[from]
	if !ok {
		edges = make(map[string]float64)
		g.out[from] = edges
	}
	edges[to] += weight
	g.outWeight[from] += weight
}

// graphSortedNodes returns the registered node ids in ascending order, so that
// any computation that iterates nodes is deterministic regardless of map order.
func (g *Graph) graphSortedNodes() []string {
	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// graphTeleport builds the teleport/restart distribution over the sorted nodes.
// When personalization is non-empty it is restricted to known nodes with
// positive weight and normalized; if that intersection is empty (or no
// personalization is given) the distribution is uniform over all nodes.
func (g *Graph) graphTeleport(ids []string, personalization map[string]float64) map[string]float64 {
	teleport := make(map[string]float64, len(ids))
	if len(personalization) > 0 {
		var total float64
		for _, id := range ids {
			if w, ok := personalization[id]; ok && w > 0 {
				teleport[id] = w
				total += w
			}
		}
		if total > 0 {
			for id := range teleport {
				teleport[id] /= total
			}
			return teleport
		}
		// Intersection empty: fall back to uniform below.
		teleport = make(map[string]float64, len(ids))
	}
	uniform := 1.0 / float64(len(ids))
	for _, id := range ids {
		teleport[id] = uniform
	}
	return teleport
}

// PageRank returns each node's PageRank score (scores sum to ~1). damping is the
// usual factor (0.85 typical); iters is the number of power-iteration steps
// (e.g. 30). personalization, when non-empty, biases the teleport/restart
// distribution toward the given nodes (values are relative weights, normalized
// internally; unknown ids ignored); when nil/empty the teleport is uniform over
// all nodes. Dangling nodes (no out-edges) redistribute their mass via the
// teleport distribution. Deterministic. Returns an empty map for an empty graph.
func (g *Graph) PageRank(damping float64, iters int, personalization map[string]float64) map[string]float64 {
	ids := g.graphSortedNodes()
	n := len(ids)
	if n == 0 {
		return map[string]float64{}
	}

	teleport := g.graphTeleport(ids, personalization)

	rank := make(map[string]float64, n)
	init := 1.0 / float64(n)
	for _, id := range ids {
		rank[id] = init
	}

	for step := 0; step < iters; step++ {
		next := make(map[string]float64, n)

		// Mass held by dangling nodes (no positive out-weight) is redistributed
		// via the teleport distribution.
		var danglingMass float64
		for _, id := range ids {
			if g.outWeight[id] <= 0 {
				danglingMass += rank[id]
			}
		}

		for _, v := range ids {
			next[v] = (1-damping)*teleport[v] + damping*danglingMass*teleport[v]
		}

		// Distribute each node's rank along its out-edges, iterating in sorted
		// order for determinism.
		for _, u := range ids {
			ow := g.outWeight[u]
			if ow <= 0 {
				continue
			}
			share := damping * rank[u] / ow
			edges := g.out[u]
			targets := make([]string, 0, len(edges))
			for t := range edges {
				targets = append(targets, t)
			}
			sort.Strings(targets)
			for _, t := range targets {
				next[t] += share * edges[t]
			}
		}

		rank = next
	}

	// Normalize so scores sum to 1 (guards against accumulated drift).
	var sum float64
	for _, id := range ids {
		sum += rank[id]
	}
	if sum > 0 && math.Abs(sum-1) > 0 {
		for _, id := range ids {
			rank[id] /= sum
		}
	}

	return rank
}
