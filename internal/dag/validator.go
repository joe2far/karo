package dag

import (
	"fmt"

	karov1alpha1 "github.com/karo-dev/karo/api/v1alpha1"
)

// ValidateNoCycles checks that the task DAG has no cycles using Kahn's algorithm.
func ValidateNoCycles(tasks []karov1alpha1.Task) error {
	inDegree := make(map[string]int)
	graph := make(map[string][]string)

	for _, t := range tasks {
		if _, exists := inDegree[t.ID]; !exists {
			inDegree[t.ID] = 0
		}
		for _, dep := range t.Deps {
			graph[dep] = append(graph[dep], t.ID)
			inDegree[t.ID]++
		}
	}

	queue := []string{}
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	visited := 0
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		visited++
		for _, neighbor := range graph[node] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}

	if visited != len(tasks) {
		return fmt.Errorf("TaskGraph contains a cycle — %d tasks are unreachable", len(tasks)-visited)
	}
	return nil
}
