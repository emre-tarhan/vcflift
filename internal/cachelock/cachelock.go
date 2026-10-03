// Package cachelock provides the advisory lock shared by all VCF Lift
// processes that mutate the application cache: reference preparation, engine
// installation, GATK runtime preparation and cache cleaning. Locks are
// deliberate and fail fast, so a second concurrent run receives ErrLocked
// instead of racing the first one on the same half-written cache.
package cachelock

import "errors"

// ErrLocked is returned when another VCF Lift process already holds the
// cache lock for the requested directory.
var ErrLocked = errors.New("another VCF Lift process is already working on this cache; wait for it to finish and try again")

// Acquire takes an exclusive lock on cacheDir through a .lock file inside it,
// creating cacheDir and the lock file when needed. The returned func releases
// the lock and must be called exactly once.
func Acquire(cacheDir string) (func(), error) {
	return acquire(cacheDir)
}
