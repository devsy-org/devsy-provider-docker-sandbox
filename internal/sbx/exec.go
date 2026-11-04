package sbx

func ExecArgs(name, program string) []string {
	return []string{"exec", "--interactive", "--user", "root", "--workdir", "/", name, "--", "/bin/sh", "-c", program}
}

const SessionEnv = "DEVSY_SBX_SESSION"

const terminateSessionScript = `tag="DEVSY_SBX_SESSION=$1"; self=$$; for sig in TERM KILL; do for d in /proc/[0-9]*; do pid=${d#/proc/}; [ "$pid" = "$self" ] && continue; if tr '\0' '\n' < "$d/environ" 2>/dev/null | grep -qxF -- "$tag"; then kill -$sig "$pid" 2>/dev/null; fi; done; [ "$sig" = TERM ] && sleep 1; done; exit 0`

func SessionExecArgs(name, program, session string) []string {
	return []string{"exec", "--interactive", "--user", "root", "--workdir", "/", "--env", SessionEnv + "=" + session, name, "--", "/bin/sh", "-c", program}
}

func TerminateSessionArgs(name, session string) []string {
	return []string{"exec", "--user", "root", "--workdir", "/", name, "--", "/bin/sh", "-c", terminateSessionScript, "sh", session}
}
