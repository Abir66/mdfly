package cmd

// ExitCode maps a command error to a process exit code. This is a placeholder:
// the full error-taxonomy table (user error = 2, server = 5, etc.) lands in
// S28. Until then, nil means success and everything else is a generic failure.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	return 1
}
