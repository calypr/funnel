package datastore

import (
	"context"

	"cloud.google.com/go/datastore"
	"github.com/ohsu-comp-bio/funnel/config"
	"google.golang.org/api/option"
)

// Datastore provides a task database on Google Cloud Datastore.
type Datastore struct {
	client *datastore.Client
}

// NewDatastore returns a new Datastore instance with the given config.
func NewDatastore(conf *config.Datastore) (*Datastore, error) {
	ctx := context.Background()

	opts := []option.ClientOption{}
	if conf.CredentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(conf.CredentialsFile))
	}

	client, err := datastore.NewClient(ctx, conf.Project, opts...)
	if err != nil {
		return nil, err
	}
	return &Datastore{client}, nil
}

// Init is a noop action for Datastore.
func (db *Datastore) Init() error {
	return nil
}

func (db *Datastore) Close() {
	db.client.Close()
}
