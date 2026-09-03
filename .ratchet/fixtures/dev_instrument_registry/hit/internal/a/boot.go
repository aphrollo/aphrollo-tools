package a

import "os"

func armed() bool {
	return os.Getenv("APHROLLO_UNLISTED") != ""
}
