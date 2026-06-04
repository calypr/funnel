package util

import (
	"os"
	"path/filepath"

	"dario.cat/mergo"
	"github.com/ohsu-comp-bio/funnel/config"
	"google.golang.org/protobuf/proto"
)

// MergeConfigFileWithFlags is a util used by server commands that use flags to set
// Funnel config values. These commands can also take in the path to a Funnel config file.
// This function ensures that the config gets set up properly. Flag values override values in
// the provided config file.
func MergeConfigFileWithFlags(file string, flagConf *config.Config) (*config.Config, error) {
	conf := config.DefaultConfig()

	// Only parse file if it exists
	if file != "" {
		fileConf := config.EmptyConfig()
		err := config.ParseFile(file, fileConf)
		if err != nil {
			return conf, err
		}

		// Use proto.Merge to properly merge nested fields
		proto.Merge(conf, fileConf)
	}

	// Merge defaults into file config (file values take priority, including false values)
	defaults := config.DefaultConfig()
	err := mergo.Merge(conf, defaults, mergo.WithoutDereference)
	if err != nil {
		return conf, err
	}

	// Merge flags into result (flags take priority over everything)
	err = mergo.Merge(conf, flagConf, mergo.WithOverride)
	if err != nil {
		return conf, err
	}

	if conf.Server.RPCAddress() != defaults.Server.RPCAddress() {
		if conf.Server.RPCAddress() != conf.RPCClient.ServerAddress {
			conf.RPCClient.ServerAddress = conf.Server.RPCAddress()
		}
	}

	return conf, nil
}

// TempConfigFile writes the configuration to a temporary file.
// Returns:
// - "path" is the path of the file.
// - "cleanup" can be called to remove the temporary file.
func TempConfigFile(c *config.Config, name string) (path string, cleanup func()) {
	tmpdir, err := os.MkdirTemp("", "")
	if err != nil {
		panic(err)
	}

	cleanup = func() {
		os.RemoveAll(tmpdir)
	}

	p := filepath.Join(tmpdir, name)
	err = config.ToYamlFile(c, p)
	if err != nil {
		panic(err)
	}
	return p, cleanup
}
