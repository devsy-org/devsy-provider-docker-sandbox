package sbx

const (
	jsonFormat = "json"
	formatArg  = "--format"
	execArg    = "exec"
	userArg    = "--user"
	rootUser   = "root"
	workdirArg = "--workdir"
	shellPath  = "/bin/sh"
)

func ExecArgs(name, program string) []string {
	return []string{
		execArg,
		"--interactive",
		userArg,
		rootUser,
		workdirArg,
		"/",
		name,
		"--",
		shellPath,
		"-c",
		program,
	}
}

const SessionEnv = "DEVSY_SBX_SESSION"

const terminateSessionScript = `tag="DEVSY_SBX_SESSION=$1"` +
	`; self=$$; for sig in TERM KILL; do for d in /proc/[0-9]*; do pid` +
	`=${d#/proc/}; [ "$pid" = "$self" ] && continue; if tr '\0' '\n' <` +
	` "$d/environ" 2>/dev/null | grep -qxF -- "$tag"; then kill -$sig ` +
	`"$pid" 2>/dev/null; fi; done` +
	`; [ "$sig" = TERM ] && sleep 1; done; exit 0`

func SessionExecArgs(name, program, session string) []string {
	return []string{
		execArg,
		"--interactive",
		userArg,
		rootUser,
		workdirArg,
		"/",
		"--env",
		SessionEnv + "=" + session,
		name,
		"--",
		shellPath,
		"-c",
		program,
	}
}

func TerminateSessionArgs(name, session string) []string {
	return []string{
		execArg,
		userArg,
		rootUser,
		workdirArg,
		"/",
		name,
		"--",
		shellPath,
		"-c",
		terminateSessionScript,
		"sh",
		session,
	}
}
