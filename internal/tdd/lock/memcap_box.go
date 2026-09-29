package lock

import (
	"strconv"
	"strings"
)

// parseMeminfoMB reads /proc/meminfo text into a MemBox. A key that is
// missing or unparsable stays 0, which every rule reads as unknown.
func parseMeminfoMB(data string) MemBox {
	var box MemBox
	for line := range strings.SplitSeq(data, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(val)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			box.RAMMB = kb / 1024
		case "MemAvailable":
			box.AvailMB = kb / 1024
		case "SwapTotal":
			box.SwapTotalMB = kb / 1024
		case "SwapFree":
			box.SwapFreeMB = kb / 1024
		}
	}
	return box
}
