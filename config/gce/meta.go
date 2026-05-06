package gce

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/imdario/mergo"
	"github.com/ohsu-comp-bio/funnel/config"
)

// WithMetadataConfig loads config from a GCE VM environment, in particular
// loading metadata from GCE's instance metadata.
// https://cloud.google.com/compute/docs/storing-retrieving-metadata
func WithMetadataConfig(conf *config.Config, meta *Metadata) (*config.Config, error) {
	defaultHostName := conf.Server.HostName

	// Load full config doc from metadata
	if meta.Instance.Attributes.FunnelConfig != "" {
		mconf := &config.Config{}
		var err error
		b := []byte(meta.Instance.Attributes.FunnelConfig)
		err = config.Parse(b, mconf)
		if err != nil {
			return conf, err
		}
		err = mergo.MergeWithOverwrite(conf, mconf)
		if err != nil {
			return conf, err
		}
	}

	// Is this a worker node? If so, inherit the node ID from the GCE instance name.
	if meta.Instance.Attributes.FunnelNodeServerAddress != "" {
		if conf.Node.ID == "" {
			conf.Node.ID = meta.Instance.Name
		}
		parts := strings.SplitN(meta.Instance.Attributes.FunnelNodeServerAddress, ":", 2)
		conf.Server.HostName = parts[0]
		if len(parts) == 2 {
			conf.Server.RPCPort = parts[1]
		}
	}

	// If the configuration contains a node ID, assume that a node
	// process should be started (instead of a server).
	if conf.Node.ID != "" {
		conf.GoogleStorage = &config.GoogleCloudStorage{}
	}

	// Auto detect the server's host name when it's not already set.
	// This makes server deployment and configuration a bit easier.
	// TODO will this work across zones?
	if conf.Server.HostName == defaultHostName && meta.Instance.Hostname != "" {
		conf.Server.HostName = meta.Instance.Hostname
	}

	return conf, nil
}

// Metadata contains a subset of details available from GCE VM metadata.
type Metadata struct {
	Instance struct {
		Name       string
		Hostname   string
		Zone       string
		Attributes struct {
			FunnelConfig            string `json:"funnel-config"`
			FunnelNodeServerAddress string `json:"funnel-node-serveraddress"`
		}
	}
	Project struct {
		ProjectID string `json:"projectId"`
	}
}

// LoadMetadata loads metadata from the GCE VM metadata server.
func LoadMetadata() (*Metadata, error) {
	return LoadMetadataFromURL("http://metadata.google.internal")
}

// LoadMetadataFromURL loads metadata from the given URL.
func LoadMetadataFromURL(url string) (*Metadata, error) {
	meta := &Metadata{}
	client := http.Client{}
	path := "/computeMetadata/v1/?recursive=true"
	req, err := http.NewRequest("GET", url+path, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Metadata-Flavor", "Google")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Non-200 response status from GCE Metadata: %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	perr := json.Unmarshal(body, meta)
	if perr != nil {
		return nil, perr
	}
	return meta, nil
}
