package config

import (
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
)

// Duration is a wrapper type around durationpb.Duration to provide compatibility
// with pflag and text (un)marshaling.
type Duration durationpb.Duration

// String returns the string representation of the duration.
func (d *Duration) String() string {
	return (*durationpb.Duration)(d).String()
}

// UnmarshalText parses text into a duration value.
func (d *Duration) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		return nil
	}
	dur, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(*durationpb.New(dur))
	return nil
}

// MarshalText converts a duration to text.
func (d *Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// Set sets the duration from the given string.
// Implements the pflag.Value interface.
func (d *Duration) Set(raw string) error {
	return d.UnmarshalText([]byte(raw))
}

// Type returns the name of this type.
// Implements the pflag.Value interface.
func (d *Duration) Type() string {
	return "duration"
}

// AsProto returns the underlying *durationpb.Duration for use in Protobuf messages.
func (d *Duration) AsProto() *durationpb.Duration {
	return (*durationpb.Duration)(d)
}
