package smoke_tests

import (
	"os/exec"
)

func ExecuteInSink(cmd string) {
	// Vulnerable Sink
	exec.Command("sh", "-c", cmd).Run()
}
