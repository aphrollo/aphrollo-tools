level: patch

`aphrollo update` and `gate doctor` judge the PATH a new shell gets (the machine and user PATH where Windows keeps them) when they say what typing `aphrollo` runs, and update says it once after its init has converged that PATH. A shell opened before the change no longer reads as a PATH to fix: the line says a new shell runs the install.
