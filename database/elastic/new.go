package elastic

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types"
	"github.com/ohsu-comp-bio/funnel/compute/scheduler"
	"github.com/ohsu-comp-bio/funnel/config"
)

var (
	minimalInclude = []string{"id", "state", "owner"}
	basicExclude   = []string{"inputs.content", "logs.logs.stderr", "logs.logs.stdout", "logs.system_logs"}
)

// Elastic provides an elasticsearch database server backend.
type Elastic struct {
	scheduler.UnimplementedSchedulerServiceServer
	client    *elasticsearch.TypedClient
	conf      *config.Elastic
	taskIndex string
	nodeIndex string
}

// NewElastic returns a new Elastic instance.
func NewElastic(conf *config.Elastic) (*Elastic, error) {
	client, err := elasticsearch.NewTypedClient(elasticsearch.Config{
		Addresses:    []string{conf.URL}, // A list of Elasticsearch nodes to use.
		Username:     conf.Username,      // Username for HTTP Basic Authentication.
		Password:     conf.Password,      // Password for HTTP Basic Authentication.
		CloudID:      conf.CloudID,       // Endpoint for the Elastic Service (https://elastic.co/cloud).
		APIKey:       conf.APIKey,        // Base64-encoded token for authorization; if set, overrides username/password and service token.
		ServiceToken: conf.ServiceToken,  // Service token for authorization; if set, overrides username/password.
	})
	if err != nil {
		return nil, err
	}
	es := &Elastic{
		client:    client,
		conf:      conf,
		taskIndex: conf.IndexPrefix + "-tasks",
		nodeIndex: conf.IndexPrefix + "-nodes",
	}
	return es, nil
}

// Close closes the database client.
func (es *Elastic) Close() {
	// no-op
}

func (es *Elastic) initIndex(ctx context.Context, name string, properties *map[string]types.Property) error {
	exists, err := es.client.Indices.Exists(name).Do(ctx)
	if err == nil && !exists {
		var mappings *types.TypeMapping = nil
		if properties != nil {
			mappings = &types.TypeMapping{
				Properties: *properties,
			}
		}
		_, err = es.client.Indices.Create(name).Mappings(mappings).Do(ctx)
	}
	return err
}

// Init creates the Elasticsearch indices.
func (es *Elastic) Init() error {
	ctx := context.Background()

	taskProperties := &map[string]types.Property{
		"id":     types.KeywordProperty{},
		"state":  types.KeywordProperty{},
		"owner":  types.KeywordProperty{},
		"inputs": types.NestedProperty{},
		"logs": types.NestedProperty{
			Properties: map[string]types.Property{
				"logs": types.NestedProperty{},
			},
		},
	}

	if err := es.initIndex(ctx, es.taskIndex, taskProperties); err != nil {
		return err
	}
	if err := es.initIndex(ctx, es.nodeIndex, nil); err != nil {
		return err
	}
	return nil
}
