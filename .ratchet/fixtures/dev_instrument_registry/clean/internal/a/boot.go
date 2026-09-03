package a

import "os"

func armed() string {
	return os.Getenv("APHROLLO_UNLISTED")
}
