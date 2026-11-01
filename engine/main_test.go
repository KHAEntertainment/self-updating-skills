package engine_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/KHAEntertainment/self-updating-skills/internal/source"
)

// upkeepBin is the real `upkeep` binary, built once in TestMain with the
// real PATH and an explicit VCS strategy, so no test can reach a fake git
// during the build and no ambient GOFLAGS are required (F4).
var upkeepBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "upkeep-engine-state")
	if err != nil {
		panic(err)
	}
	// Isolate the default state database: no test reaches the user's real
	// ~/.agents/upkeep/state.db unless it explicitly sets its own path.
	os.Setenv("UPKEEP_STATE_DB", filepath.Join(dir, "state.db"))

	root := moduleRoot()
	upkeepBin = filepath.Join(dir, "upkeep-test-bin")
	// The test binary enables the HTTP seam via -ldflags -X, so spawned
	// `upkeep` processes (barrier, takeover, poll-skip tests) accept plain
	// httptest hosts. Release builds never set it (C1).
	ldflags := "-X github.com/KHAEntertainment/self-updating-skills/internal/source.AllowInsecureHTTP=engine-test"
	cmd := exec.Command("go", "build", "-buildvcs=false", "-ldflags", ldflags, "-o", upkeepBin, "./cmd/upkeep")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build upkeep: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	// Test-only HTTP seam for the whole engine suite (plain httptest
	// hosts). The source package manages its own seam in its tests.
	source.AllowInsecureHTTP = "engine-test"

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// moduleRoot walks up from the working directory to the module root.
func moduleRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	panic("go.mod not found above " + wd)
}
