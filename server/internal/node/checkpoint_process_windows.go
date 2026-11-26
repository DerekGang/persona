//go:build windows

package node

// Windows has no portable signal-0 check here. Keeping a possibly-dead lock is safer than deleting a live process's
// temporary index; cleanup still handles legacy names by age.
func checkpointProcessAlive(pid int) bool {
	return pid > 0
}
