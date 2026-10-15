package dockerutil

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/moby/moby/client"
)

var dockerServerAPIVersionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)server API version:\s*([0-9]+\.[0-9]+)`),
	regexp.MustCompile(`(?i)maximum supported API version is\s*([0-9]+\.[0-9]+)`),
}

// NewDockerClient returns a new docker client. This util handles
// working around some client/server API version mismatch issues.
func NewDockerClient() (*client.Client, error) {
	dclient, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}

	// If the api version is not set test if the client can communicate with the
	// server; if not infer API version from error message and inform the client
	// to use that version for future communication
	if os.Getenv("DOCKER_API_VERSION") == "" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()

		_, err := dclient.ServerVersion(ctx, client.ServerVersionOptions{})
		if err != nil {
			version := dockerServerAPIVersion(err.Error())
			if version == "" {
				return nil, fmt.Errorf("can't connect Docker client: %w", err)
			}
			if err := os.Setenv("DOCKER_API_VERSION", version); err != nil {
				return nil, fmt.Errorf("setting Docker API version %q: %w", version, err)
			}
			return NewDockerClient()
		}
	}
	return dclient, nil
}

func dockerServerAPIVersion(message string) string {
	for _, pattern := range dockerServerAPIVersionPatterns {
		if match := pattern.FindStringSubmatch(message); len(match) == 2 {
			return match[1]
		}
	}
	return ""
}
