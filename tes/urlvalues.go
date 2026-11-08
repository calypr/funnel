package tes

import (
	"fmt"
	"net/url"
)

func addString(u url.Values, key, value string) {
	if value != "" {
		u.Add(key, value)
	}
}

func addInt32(u url.Values, key string, value int32) {
	if value != 0 {
		u.Add(key, fmt.Sprint(value))
	}
}
