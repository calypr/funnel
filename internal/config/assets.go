package config

import "embed"

//go:embed templates/hpc examples
var bundled embed.FS

func embeddedText(name string) string {
	data, err := bundled.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(data)
}
