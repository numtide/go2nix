// Package toposort provides a generic topological sort with cycle detection.
package toposort

import (
	"fmt"
	"sort"
)

// Sort performs a topological sort over a set of named nodes.
// deps returns the dependency keys for a given key (may include keys not in nodes).
// Returns nodes in dependency order (leaves first), or an error on cycles.
func Sort[T any](nodes map[string]T, deps func(key string) []string) ([]T, error) {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)

	state := make(map[string]int, len(nodes))
	var result []T

	var visit func(string) error
	visit = func(key string) error {
		switch state[key] {
		case visited:
			return nil
		case visiting:
			return fmt.Errorf("import cycle detected involving %s", key)
		}
		state[key] = visiting

		for _, dep := range deps(key) {
			if err := visit(dep); err != nil {
				return err
			}
		}

		state[key] = visited
		if node, ok := nodes[key]; ok {
			result = append(result, node)
		}
		return nil
	}

	keys := make([]string, 0, len(nodes))
	for k := range nodes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if err := visit(k); err != nil {
			return nil, err
		}
	}

	return result, nil
}
