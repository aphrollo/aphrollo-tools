package cli

func Run(args []string) int {
	switch args[0] {
	case "dothing":
		return 0
	case "alsothis", "alias":
		return 0
	}
	return 1
}
