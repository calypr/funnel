package util

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// CheckHTTPResponse does some basic error handling
// and reads the response body into a byte array
func CheckHTTPResponse(resp *http.Response, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if (resp.StatusCode / 100) != 2 {
		status := http.StatusText(resp.StatusCode)
		if status == "" {
			status = "Unknown Status"
		}
		if len(body) == 0 {
			return nil, fmt.Errorf("[STATUS CODE - %d] %s", resp.StatusCode, status)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) == nil && len(fields) == 1 {
			for _, key := range []string{"error", "message"} {
				var message string
				if value, ok := fields[key]; ok && json.Unmarshal(value, &message) == nil && message != "" {
					return nil, fmt.Errorf("[STATUS CODE - %d] %s: %s", resp.StatusCode, status, message)
				}
			}
		}
		return nil, fmt.Errorf("[STATUS CODE - %d] %s: %s", resp.StatusCode, status, body)
	}
	return body, nil
}
