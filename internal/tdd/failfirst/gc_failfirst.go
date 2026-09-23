package failfirst

// gcOriginFile records, beside a hash-named gate directory, which repo root
// it belongs to -- the only way to tell a live gate dir from the remains of
// a repo that was deleted months ago.
const gcOriginFile = "origin.txt"
