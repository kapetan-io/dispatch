package config

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Rules is the [rules] table. Rules read it as ctx.config.rules.
type Rules map[string]any

// Defaults of the five keys `dispatch rules init` writes.
const (
	DefaultWorkerModel            = "sonnet"
	DefaultDesignModel            = "opus"
	DefaultLaunchFailureLimit     = 2
	DefaultNudgeLimit             = 2
	DefaultBlockedRenotifyMinutes = 15
)

// ShippedRules is the [rules] table with the five keys the shipped rules read, at today's values.
func ShippedRules() Rules {
	return Rules{
		"worker_model":             DefaultWorkerModel,
		"design_model":             DefaultDesignModel,
		"launch_failure_limit":     int64(DefaultLaunchFailureLimit),
		"nudge_limit":              int64(DefaultNudgeLimit),
		"blocked_renotify_minutes": int64(DefaultBlockedRenotifyMinutes),
	}
}

func (r Rules) check() error {
	for _, key := range []string{"launch_failure_limit", "nudge_limit", "blocked_renotify_minutes"} {
		if v, ok := r[key]; ok {
			if _, isInt := v.(int64); !isInt {
				return fmt.Errorf("rules.%s must be a whole number, got %v", key, v)
			}
		}
	}
	for _, key := range []string{"worker_model", "design_model", "worker", "design"} {
		if v, ok := r[key]; ok {
			if _, isString := v.(string); !isString {
				return fmt.Errorf("rules.%s must be a string, got %v", key, v)
			}
		}
	}
	return nil
}

func (r Rules) integer(key string, fallback int) int {
	if v, ok := r[key].(int64); ok {
		return int(v)
	}
	return fallback
}

func (r Rules) text(key, fallback string) string {
	if v, ok := r[key].(string); ok {
		return v
	}
	return fallback
}

// WorkerModel is the model build workers start with.
func (c Config) WorkerModel() string { return c.Rules.text("worker_model", DefaultWorkerModel) }

// Duration is a time.Duration that reads from TOML text such as "5m" or "7d".
type Duration time.Duration

var days = regexp.MustCompile(`^(\d+)d(.*)$`)

// ParseDuration is time.ParseDuration plus a leading whole number of days.
func ParseDuration(text string) (time.Duration, error) {
	var extra time.Duration
	if m := days.FindStringSubmatch(text); m != nil {
		n, _ := strconv.Atoi(m[1])
		extra, text = time.Duration(n)*24*time.Hour, m[2]
		if text == "" {
			return extra, nil
		}
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, err
	}
	return extra + d, nil
}

// UnmarshalText reads a duration such as "5m", "6h" or "7d".
func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("%q is not a duration such as \"5m\" or \"7d\"", string(text))
	}
	*d = Duration(parsed)
	return nil
}

// MarshalText writes the duration in the form UnmarshalText reads.
func (d Duration) MarshalText() ([]byte, error) {
	v := time.Duration(d)
	if v > 0 && v%(24*time.Hour) == 0 {
		return []byte(strconv.Itoa(int(v/(24*time.Hour))) + "d"), nil
	}
	return []byte(v.String()), nil
}
