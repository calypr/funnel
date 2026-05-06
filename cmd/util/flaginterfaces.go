package util

import (
	"fmt"
	"strconv"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"google.golang.org/protobuf/types/known/durationpb"
)

// TimeoutConfigValue is a pflag.Value implementation for config.TimeoutConfig.
type TimeoutConfigValue struct {
	TC **config.TimeoutConfig // Pointer to a pointer to handle struct fields
}

// String returns the string representation of the TimeoutConfig.
func (tcv *TimeoutConfigValue) String() string {
	if tcv.TC == nil || *tcv.TC == nil {
		return ""
	}
	if disabled := (*tcv.TC).GetDisabled(); disabled {
		return "-1s"
	}
	if duration := (*tcv.TC).GetDuration(); duration != nil {
		return time.Duration(duration.GetSeconds()*1e9 + int64(duration.GetNanos())).String()
	}
	return ""
}

// Set parses a string into a config.TimeoutConfig.
func (tcv *TimeoutConfigValue) Set(s string) error {
	if tcv.TC == nil {
		return fmt.Errorf("TimeoutConfigValue.TC is nil")
	}
	if s == "-1s" {
		*tcv.TC = &config.TimeoutConfig{
			TimeoutOption: &config.TimeoutConfig_Disabled{
				Disabled: true,
			},
		}
		return nil
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		// fallback to int seconds
		if seconds, err := strconv.ParseInt(s, 10, 64); err == nil {
			dur = time.Duration(seconds) * time.Second
		} else {
			return err
		}
	}
	*tcv.TC = &config.TimeoutConfig{
		TimeoutOption: &config.TimeoutConfig_Duration{
			Duration: durationpb.New(dur),
		},
	}
	return nil
}

// Type returns the flag type.
func (tcv *TimeoutConfigValue) Type() string {
	return "TimeoutConfig"
}

// DurationValue is a pflag.Value implementation for durationpb.Duration.
type DurationValue struct {
	D **durationpb.Duration // Pointer to a pointer to handle struct fields
}

// String returns the string representation of the duration.
func (d *DurationValue) String() string {
	if d.D == nil || *d.D == nil {
		return ""
	}
	return time.Duration((*d.D).Seconds*1e9 + int64((*d.D).Nanos)).String()
}

// Set parses a string into a durationpb.Duration.
func (d *DurationValue) Set(s string) error {
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	if d.D == nil {
		return fmt.Errorf("DurationValue.D is nil")
	}
	*d.D = durationpb.New(dur)
	return nil
}

// Type returns the flag type.
func (d *DurationValue) Type() string {
	return "Duration"
}
