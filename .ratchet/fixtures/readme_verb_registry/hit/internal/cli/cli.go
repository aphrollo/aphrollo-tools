package cli

func Run(args []string) int {
	switch args[0] {
	case "newverb":
		return 0
	}
	return 1
}
