package tddtest

import "runtime"

// hostGOOS is the OS the fixtures build executables for, read once here so a
// helper that names a binary never compares runtime.GOOS inline.
var hostGOOS = runtime.GOOS
