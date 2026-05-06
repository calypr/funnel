package events

import (
	"context"

	"cloud.google.com/go/pubsub"
	"github.com/ohsu-comp-bio/funnel/config"
	oldctx "golang.org/x/net/context"
	"google.golang.org/api/option"
)

// PubSubWriter writes events to Google Cloud Pub/Sub.
type PubSubWriter struct {
	topic *pubsub.Topic
}

// NewPubSubWriter creates a new PubSubWriter.
//
// The given context is used to shut down the Pub/Sub client and flush
// any buffered messages.
//
// Stdout, stderr, and system log events are not sent.
func NewPubSubWriter(ctx context.Context, conf *config.PubSub) (*PubSubWriter, error) {
	opts := []option.ClientOption{}
	if conf.CredentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(conf.CredentialsFile))
	}

	client, err := pubsub.NewClient(ctx, conf.Project, opts...)
	if err != nil {
		return nil, err
	}

	topic := client.Topic(conf.Topic)
	ok, err := topic.Exists(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		topic, err = client.CreateTopic(ctx, conf.Topic)
		if err != nil {
			return nil, err
		}
	}

	go func() {
		<-ctx.Done()
		topic.Stop()
	}()
	return &PubSubWriter{topic}, nil
}

// WriteEvent writes an event to the configured Pub/Sub topic.
// Events are buffered and sent in batches by a background routine.
// Stdout, stderr, and system log events are not sent.
func (p *PubSubWriter) WriteEvent(ctx context.Context, ev *Event) error {
	switch ev.Type {
	case Type_EXECUTOR_STDOUT, Type_EXECUTOR_STDERR, Type_SYSTEM_LOG:
		return nil
	}

	s, err := Marshal(ev)
	if err != nil {
		return err
	}

	p.topic.Publish(ctx, &pubsub.Message{
		Data: []byte(s),
	})
	return nil
}

func (p *PubSubWriter) Close() {}

// ReadPubSub reads events from the topic configured by "conf".
// The subscription "subname" will be created if it doesn't exist.
// This blocks until the context is canceled.
func ReadPubSub(ctx context.Context, conf *config.PubSub, subname string, w Writer) error {
	cl, err := pubsub.NewClient(ctx, conf.Project)
	if err != nil {
		return err
	}

	sub := cl.Subscription(subname)
	ok, err := sub.Exists(ctx)
	if err != nil {
		return err
	}
	if !ok {
		topic := cl.Topic(conf.Topic)

		sub, err = cl.CreateSubscription(ctx, subname, pubsub.SubscriptionConfig{
			Topic: topic,
		})
		if err != nil {
			return err
		}
	}

	sub.Receive(ctx, func(ctx oldctx.Context, m *pubsub.Message) {
		ev := &Event{}
		Unmarshal(m.Data, ev)
		if err != nil {
			return
		}
		w.WriteEvent(context.Background(), ev)
		m.Ack()
	})

	return nil
}
