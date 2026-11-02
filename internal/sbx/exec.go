package sbx

func ExecArgs(name, program string) []string {
	return []string{"exec", "--interactive", "--user", "root", "--workdir", "/", name, "--", "/bin/sh", "-c", program}
}
