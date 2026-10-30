package datastore

import (
	"context"
	"log"

	"cloud.google.com/go/datastore"
	"github.com/calypr/funnel/config"
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
	err := db.client.Close()
	if err != nil {
		log.Printf("closing Datastore client: %v", err)
	}
}
