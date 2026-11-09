package util

import (
	"fmt"
	"os"
	"strings"

	"dario.cat/mergo"
	"google.golang.org/protobuf/proto"

	"github.com/calypr/funnel/api/config"
	appconfig "github.com/calypr/funnel/internal/config"
)

// MergeConfigFileWithFlags is a util used by server commands that use flags to set
// Funnel config values. These commands can also take in the path to a Funnel config file.
// This function ensures that the config gets set up properly. Flag values override values in
// the provided config file. FUNNEL_CONFIG supplies the file when none is supplied;
// FUNNEL_LOG_LEVEL overrides the file's log level, but explicit flags take priority.
func MergeConfigFileWithFlags(file string, flagConf *config.Config) (*config.Config, error) {
	conf := appconfig.DefaultConfig()
	if file == "" {
		file = os.Getenv("FUNNEL_CONFIG")
	}

	// Only parse file if it exists
	if file != "" {
		fileConf := appconfig.EmptyConfig()
		err := appconfig.ParseFile(file, fileConf)
		if err != nil {
			return conf, err
		}

		// Use proto.Merge to properly merge nested fields
		proto.Merge(conf, fileConf)

		// Repeated fields are appended by proto.Merge. ForbiddenPathPrefixes is
		// an authoritative configuration list, so a non-empty value from the
		// config file must replace the Kubernetes runtime defaults instead.
		if fileConf.Kubernetes != nil && len(fileConf.Kubernetes.ForbiddenPathPrefixes) > 0 {
			conf.Kubernetes.ForbiddenPathPrefixes = append([]string(nil), fileConf.Kubernetes.ForbiddenPathPrefixes...)
		}
	}

	// Merge defaults into file config (file values take priority, including false values)
	defaults := appconfig.DefaultConfig()
	err := mergo.Merge(conf, defaults, mergo.WithoutDereference)
	if err != nil {
		return conf, err
	}

	if level := os.Getenv("FUNNEL_LOG_LEVEL"); level != "" {
		conf.Logger.Level = level
	}

	// Merge flags into result (flags take priority over everything)
	err = mergo.Merge(conf, flagConf, mergo.WithOverride)
	if err != nil {
		return conf, err
	}
	if strings.HasPrefix(conf.GetWorker().GetWorkDir(), "~") {
		return conf, fmt.Errorf("Worker.WorkDir %q starts with ~, which is not expanded; use an explicit path", conf.Worker.WorkDir)
	}

	if appconfig.RPCAddress(conf.Server) != appconfig.RPCAddress(defaults.Server) {
		if appconfig.RPCAddress(conf.Server) != conf.RPCClient.ServerAddress {
			conf.RPCClient.ServerAddress = appconfig.RPCAddress(conf.Server)
		}
	}

	return conf, nil
}
