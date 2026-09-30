package pkg

type node struct{ next *node }

// lastNode walks a linked list on a nil check alone: invert the condition or
// drop the step and the loop never ends.
func lastNode(head *node) *node {
	cur := head
	for cur != nil && cur.next != nil {
		cur = cur.next
	}
	return cur
}

// drain empties a work queue that its own body refills.
func drain(queue []*node) int {
	n := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.next != nil {
			queue = append(queue, cur.next)
		}
		n++
	}
	return n
}

// climb walks parents with a bare truthiness condition.
func climb(dir string, parent func(string) string) string {
	for dir != "" {
		dir = parent(dir)
	}
	return dir
}
