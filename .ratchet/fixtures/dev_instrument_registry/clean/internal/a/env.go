package a

import "os/exec"

func withFlag(cmd *exec.Cmd) {
	cmd.Env = append(cmd.Env, "APHROLLO_EXEC_FLAG=1")
}
