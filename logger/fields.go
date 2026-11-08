package logger

import "fmt"

// ArgListToMap converts an argument list to a map, e.g.
// ("key", value, "key2", value2) => {"key": value, "key2", value2}
func ArgListToMap(args ...interface{}) map[string]interface{} {
	f := make(map[string]interface{}, len(args)/2)

	if len(args) == 1 {
		f["unknown"] = args[0]
		return f
	}

	if len(args)%2 != 0 {
		f["unknown"] = args[len(args)-1]
		args = args[:len(args)-1]
	}

	for i := 0; i < len(args); i += 2 {
		k := fmt.Sprintf("%v", args[i])
		v := args[i+1]
		f[k] = v
	}

	return f
}
