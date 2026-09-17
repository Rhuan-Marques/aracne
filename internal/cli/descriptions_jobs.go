package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// `arac descriptions jobs` -- what the background workers are doing, and how to stop them.
//
// WHY IT IS A SHIPPED COMMAND rather than a debugging aid. A detached worker has no terminal:
// its stdout and stderr go to /dev/null by construction, because a child holding the parent's
// stdout keeps a hook or a Bash tool call waiting for EOF, which would hang the very read the
// worker exists to free. So the claim table is the only place a worker can say anything, and
// without a way to read it the honest description of the feature is "it starts processes you
// cannot see and cannot stop". That is not a feature anyone should ship.
//
// --stop is cooperative cancellation THROUGH THE TABLE, not a signal. It needs no pid (which may
// have been recycled), no permission to signal another process, and no platform-specific code;
// the owning worker notices on its next heartbeat, within DescriptionJobHeartbeatInterval, and
// stops in a way that settles its claims instead of orphaning them.
func RunDescriptionJobs(args []string) {
	fs := flag.NewFlagSet("descriptions-jobs", flag.ExitOnError)
	stop := fs.String("stop", "", "Ask a job to stop, by id, or `--stop all` for every running job")
	fs.Parse(args)

	manager, _ := InitRegistry(ProjectDBPath(DefaultDBRelative))
	dbPath := manager.DbPath()

	if strings.TrimSpace(*stop) != "" {
		jobID := strings.TrimSpace(*stop)
		if jobID == "all" {
			jobID = ""
		}
		n, err := helper.RequestDescriptionJobCancel(dbPath, jobID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if n == 0 {
			fmt.Println("No running description jobs to stop.")
			return
		}
		fmt.Printf("Asked %d claimed resource(s) to stop. Their workers exit within %s.\n",
			n, helper.DescriptionJobHeartbeatInterval)
		return
	}

	jobs, err := helper.ListDescriptionJobs(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(jobs) == 0 {
		fmt.Println("No description jobs.")
		return
	}

	now := time.Now()
	fmt.Printf("%-14s %-8s %-9s %-8s %s\n", "JOB", "PID", "STATE", "AGE", "RESOURCE")
	for _, j := range jobs {
		state := j.State
		// A row can say `running` while its worker is long gone -- that is exactly what the
		// staleness rule is for, and printing the raw column would tell a reader the opposite
		// of the truth about whether anything is still generating.
		if j.State == helper.DescriptionJobRunning && !j.Live(now) {
			state = "stale"
		}
		age := now.Sub(j.HeartbeatAt).Truncate(time.Second)
		line := fmt.Sprintf("%-14s %-8d %-9s %-8s %s",
			truncateJobID(j.JobID), j.PID, state, age, j.ResourceID)
		if detail := strings.TrimSpace(j.Detail); detail != "" {
			line += "  -- " + detail
		}
		fmt.Println(line)
	}
	fmt.Printf("\nStop them with `arac descriptions jobs --stop all`. "+
		"Failures are logged to .aracne/%s.\n", descriptionWorkerLog)
}

func truncateJobID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
