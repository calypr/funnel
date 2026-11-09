package worker

import (
	"encoding/hex"
	"strings"
)

// commandValue preserves a task string's identity in template conditions, but
// prints a shell-safe placeholder. Restore its bytes only after word splitting
// so neither a task value nor quotes around a legacy placeholder can add args.
type commandValue string

func (v commandValue) String() string {
	return "__FUNNEL_ARG_" + hex.EncodeToString([]byte(v)) + "__"
}

type commandVolume struct {
	HostPath, ContainerPath commandValue
	Readonly                bool
}

type commandInvocation struct {
	Image, Workdir commandValue
	ShellCommand   []commandValue
	Env            map[commandValue]commandValue
	Volumes        []commandVolume
}

type commandTemplateData struct {
	DockerCommand
	Command                  commandInvocation
	Id, Name, Image, Workdir commandValue
	Volumes                  []commandVolume
	Env, Tags                map[commandValue]commandValue
}

func (d commandTemplateData) GetImage() commandValue { return d.Image }

func (d DockerCommand) templateArguments() (commandTemplateData, *strings.Replacer) {
	var replacements []string
	bind := func(value string) commandValue {
		v := commandValue(value)
		replacements = append(replacements, v.String(), value)
		return v
	}
	bindMap := func(values map[string]string) (map[commandValue]commandValue, map[string]string) {
		if values == nil {
			return nil, nil
		}
		bound := make(map[commandValue]commandValue, len(values))
		formatted := make(map[string]string, len(values))
		for key, value := range values {
			k, v := bind(key), bind(value)
			bound[k] = v
			formatted[k.String()] = v.String()
		}
		return bound, formatted
	}
	env, formattedEnv := bindMap(d.Env)
	tags, formattedTags := bindMap(d.Tags)
	// Formatter methods must also see placeholders so token-like user text is
	// encoded before parsing and restored exactly once.
	d.Command.Env = formattedEnv
	d.Tags = formattedTags
	data := commandTemplateData{
		DockerCommand: d,
		Id:            bind(d.Id), Name: bind(d.Name), Image: bind(d.Image), Workdir: bind(d.Workdir),
		Env: env, Tags: tags,
	}
	for _, volume := range d.Volumes {
		data.Volumes = append(data.Volumes, commandVolume{bind(volume.HostPath), bind(volume.ContainerPath), volume.Readonly})
	}
	data.Command = commandInvocation{Image: data.Image, Workdir: bind(d.Command.Workdir), Env: env}
	for _, arg := range d.Command.ShellCommand {
		data.Command.ShellCommand = append(data.Command.ShellCommand, bind(arg))
	}
	for _, volume := range d.Command.Volumes {
		data.Command.Volumes = append(data.Command.Volumes, commandVolume{bind(volume.HostPath), bind(volume.ContainerPath), volume.Readonly})
	}
	return data, strings.NewReplacer(replacements...)
}
