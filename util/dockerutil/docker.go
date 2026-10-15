package dockerutil

import (
	"context"
	"errors"
	"os"
	"regexp"
	"time"

	"github.com/moby/moby/client"
)

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
			re := regexp.MustCompile(`([0-9\.]+)`)
			version := re.FindAllString(err.Error(), -1)
			if version == nil {
				return nil, errors.New("Can't connect docker client")
			}
			// Error message example:
			//   Error getting metadata for container: Error response from daemon: client is newer than server (client API version: 1.26, server API version: 1.24)
			os.Setenv("DOCKER_API_VERSION", version[1])
			return NewDockerClient()
		}
	}
	return dclient, nil
}
