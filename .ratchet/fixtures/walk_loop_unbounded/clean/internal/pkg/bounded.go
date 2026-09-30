package pkg

import "bufio"

type node struct{ next *node }

// lastNode bounds the walk with a step counter compared to a limit.
func lastNode(head *node, maxSteps int) *node {
	cur := head
	for steps := 0; cur != nil && steps < maxSteps; steps++ {
		cur = cur.next
	}
	return cur
}

// countUp compares its index to a bound.
func countUp(n int) int {
	i := 0
	for i < n {
		i++
	}
	return i
}

// reach consults a visited set in the loop condition.
func reach(start *node, visited map[*node]bool) int {
	n := 0
	for cur := start; cur != nil; cur = cur.next {
		if visited[cur] {
			break
		}
		visited[cur] = true
		n++
	}
	return n
}

// unseen names the visited set in its condition.
func unseen(queue []*node, seen map[*node]bool) int {
	n := 0
	for len(queue) > 0 && !seen[queue[0]] {
		seen[queue[0]] = true
		queue = queue[1:]
		n++
	}
	return n
}

// lines is bounded by its reader, not by walked state.
func lines(sc *bufio.Scanner) int {
	n := 0
	for sc.Scan() {
		n++
	}
	return n
}

// each ranges.
func each(items []*node) int {
	n := 0
	for range items {
		n++
	}
	return n
}

// shrink is proven to terminate.
func shrink(args []string) int {
	n := 0
	// walk-terminates: args loses one element every turn
	for len(args) > 0 {
		args = args[1:]
		n++
	}
	return n
}
