package install

import "fmt"

// doctorSystemPath reports, as information only, what the box's registry
// PATH would give a terminal a person opens themselves: a git or cargo ahead
// of the queue dir, or no queue dir at all. The agent's shells inherit the PATH
// this process has and are judged by doctorShimPath; a person's own terminal
// is warned about, never failed. ok=false means there is nothing to add: the
// process PATH was not read, the registry was not read, or its order is fine.
func doctorSystemPath(in DoctorInput) (DoctorCheck, bool) {
	c := DoctorCheck{Name: "shim dir on system PATH", OK: true, Info: true}
	if len(in.ProcessPathDirs) == 0 || len(in.PathDirs) == 0 {
		return c, false
	}
	shimAt := shimIndex(in.PathDirs, in.ShimDir)
	if shimAt < 0 {
		c.Detail = fmt.Sprintf("the system PATH does not carry %s; a terminal you open yourself runs a direct git or cargo unqueued", in.ShimDir)
		return c, true
	}
	if dir, name, shadowed := shadowingCommand(in.PathDirs[:shimAt]); shadowed {
		c.Detail = fmt.Sprintf("on the system PATH %s in %s comes before the queue dir %s; a terminal you open yourself runs a direct git or cargo unqueued (the agent's shells are judged on their own PATH above)",
			name, dir, in.ShimDir)
		return c, true
	}
	return c, false
}
