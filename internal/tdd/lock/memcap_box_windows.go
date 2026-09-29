//go:build windows

package lock

// readMemBox reads this box's memory from GlobalMemoryStatusEx. Available is
// commit (RAM plus pagefile not yet charged), the same quantity
// machineAvailGB reports, because that is what an allocation is refused
// against. Swap is left unknown: the pagefile is already inside the commit
// figure.
func readMemBox() MemBox {
	m, ok := globalMemoryStatus()
	if !ok {
		return MemBox{}
	}
	return MemBox{RAMMB: int64(m.totalPhys / (1 << 20)), AvailMB: int64(m.availPageFile / (1 << 20))}
}
