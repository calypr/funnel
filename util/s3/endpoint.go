package s3util

import (
	"regexp"
	"strings"
)

// ParseEndpoint "cleans" a raw URL and returns a valid S3 endpoint
// Shared between Generic_S3 and Amazon_S3 clients
func ParseEndpoint(url string) string {
	var endpointRE = regexp.MustCompile("^(http[s]?://)?(.[^/]+)(.+)?$")

	endpoint := endpointRE.ReplaceAllString(url, "$2$3")

	endpoint = strings.TrimRight(endpoint, "/")

	return endpoint
}
