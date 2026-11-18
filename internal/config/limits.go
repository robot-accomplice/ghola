package config

import (
	"fmt"
	"math"
	"time"

	flag "github.com/spf13/pflag"
)

// curlLimitFlags holds curl's transfer-limit flags in curl's units
// (seconds, bytes per second) until apply converts them (audit finding H3).
type curlLimitFlags struct {
	maxTime        float64
	connectTimeout float64
	speedLimit     int64
	speedTime      int
}

// curl's defaults when only one of --speed-limit and --speed-time is given.
const (
	defaultSpeedTime  = 30 // seconds
	defaultSpeedLimit = 1  // bytes per second
)

func (c *curlLimitFlags) register(fs *flag.FlagSet) {
	fs.Float64Var(&c.maxTime, "max-time", 0, "Maximum time in seconds for the whole transfer, as --timeout")
	fs.Float64Var(&c.connectTimeout, "connect-timeout", 0, "Maximum time in seconds for the connection phase (DNS, TCP, TLS)")
	fs.Int64Var(&c.speedLimit, "speed-limit", 0, "Stop when slower than this many bytes per second for --speed-time")
	fs.IntVar(&c.speedTime, "speed-time", 0, "Seconds below --speed-limit before the transfer stops (default 30)")
}

// apply checks the flags and the retry count, and writes the flags into r.
func (c *curlLimitFlags) apply(fs *flag.FlagSet, r *ResilienceOptions) error {
	if r.Retries < 0 {
		return fmt.Errorf("-r/--retry cannot be negative")
	}
	if c.maxTime < 0 || c.connectTimeout < 0 || c.speedLimit < 0 || c.speedTime < 0 {
		return fmt.Errorf("--max-time, --connect-timeout, --speed-limit and --speed-time cannot be negative")
	}
	if fs.Changed("max-time") {
		if fs.Changed("timeout") {
			return fmt.Errorf("--max-time and --timeout set the same limit: use one")
		}
		r.Timeout = int(math.Ceil(c.maxTime * 1000))
	}
	r.ConnectTimeout = time.Duration(c.connectTimeout * float64(time.Second))

	limit, secs := c.speedLimit, c.speedTime
	if limit > 0 && secs == 0 {
		secs = defaultSpeedTime
	}
	if secs > 0 && limit == 0 {
		limit = defaultSpeedLimit
	}
	r.SpeedLimit = limit
	r.SpeedTime = time.Duration(secs) * time.Second
	return nil
}
