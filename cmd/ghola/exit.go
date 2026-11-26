package main

import (
	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
)

// exitFor returns curl's exit code for the cause of err. The client
// decides it, because the same rule decides whether a retry may help.
func exitFor(err error) config.ExitCode { return client.ExitCodeFor(err) }
