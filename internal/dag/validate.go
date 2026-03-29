package dag

import "fmt"

// ValidateNoCycles checks for cycles in a DAG defined by a map of node ID to its dependency IDs.
// Uses Kahn's algorithm. Returns an error if a cycle is detected.
func ValidateNoCyclesFromDeps(deps map[string][]string) error {
	// Build in-degree map and adjacency list.
	inDegree := make(map[string]int)
	adj := make(map[string][]string)

	// Initialize all nodes.
	for node := range deps {
		if _, ok := inDegree[node]; !ok {
			inDegree[node] = 0
		}
		for _, dep := range deps[node] {
			adj[dep] = append(adj[dep], node)
			inDegree[node]++
			if _, ok := inDegree[dep]; !ok {
				inDegree[dep] = 0
			}
		}
	}

	// Collect nodes with in-degree 0.
	var queue []string
	for node, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, node)
		}
	}

	visited := 0
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		visited++
		for _, neighbor := range adj[node] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}

	if visited != len(inDegree) {
		return fmt.Errorf("cycle detected in task graph: processed %d of %d nodes", visited, len(inDegree))
	}
	return nil
}
