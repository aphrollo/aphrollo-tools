package main

import "go/types"

// holdsLock reports whether a value of type t carries a lock, the way go
// vet's copylocks judges it: a type whose pointer has Lock and Unlock
// methods (sync.Mutex, the noCopy marker inside sync.Map and the atomics),
// or a struct or array holding one. Copying such a value is refused by vet
// and, for a package var, silently splits one piece of state into two.
func holdsLock(t types.Type) bool {
	return holdsLockSeen(t, map[types.Type]bool{})
}

func holdsLockSeen(t types.Type, seen map[types.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	if _, isPtr := t.Underlying().(*types.Pointer); !isPtr {
		ms := types.NewMethodSet(types.NewPointer(t))
		if ms.Lookup(nil, "Lock") != nil && ms.Lookup(nil, "Unlock") != nil {
			return true
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			if holdsLockSeen(u.Field(i).Type(), seen) {
				return true
			}
		}
	case *types.Array:
		return holdsLockSeen(u.Elem(), seen)
	}
	return false
}
