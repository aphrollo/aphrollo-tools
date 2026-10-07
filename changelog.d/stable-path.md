level: patch

PATH no longer names a version. `aphrollo install` and `update` put the queue shim dir and the user-space root first on the user PATH and the agent's env.PATH, and the root's launcher follows `current`, so an update reaches every open shell and session without a restart. On Windows the shims move to `%LOCALAPPDATA%\aphrollo\cargo-queue`, the root gains an sh `aphrollo` launcher beside `aphrollo.cmd` for Git Bash, and the version directories an earlier install put on either PATH are dropped; before, the agent's env.PATH kept an older binary ahead of the newest one.
