package events

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"github.com/calypr/funnel/config"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PubSubWriter writes events to Google Cloud Pub/Sub.
type PubSubWriter struct {
	client    *pubsub.Client
	publisher *pubsub.Publisher
	shutdown  chan struct{}
	closeOnce sync.Once
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

	topicName := fmt.Sprintf("projects/%s/topics/%s", conf.Project, conf.Topic)
	_, err = client.TopicAdminClient.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: topicName})
	if status.Code(err) == codes.NotFound {
		_, err = client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{Name: topicName})
	}
	if err != nil {
		return nil, errors.Join(err, client.Close())
	}

	w := &PubSubWriter{
		client:    client,
		publisher: client.Publisher(topicName),
		shutdown:  make(chan struct{}),
	}
	go func() {
		select {
		case <-ctx.Done():
			w.Close()
		case <-w.shutdown:
		}
	}()
	return w, nil
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

	p.publisher.Publish(ctx, &pubsub.Message{Data: []byte(s)})
	return nil
}

func (p *PubSubWriter) Close() {
	p.closeOnce.Do(func() {
		close(p.shutdown)
		p.publisher.Stop()
		if err := p.client.Close(); err != nil {
			log.Printf("close Pub/Sub event writer: %v", err)
		}
	})
}

// ReadPubSub reads events from the topic configured by "conf".
// The subscription "subname" will be created if it doesn't exist.
// This blocks until the context is canceled.
func ReadPubSub(ctx context.Context, conf *config.PubSub, subname string, w Writer) (retErr error) {
	opts := []option.ClientOption{}
	if conf.CredentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(conf.CredentialsFile))
	}
	client, err := pubsub.NewClient(ctx, conf.Project, opts...)
	if err != nil {
		return err
	}
	defer func() {
		if err := client.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close Pub/Sub event reader: %w", err))
		}
	}()

	subscriptionName := fmt.Sprintf("projects/%s/subscriptions/%s", conf.Project, subname)
	_, err = client.SubscriptionAdminClient.GetSubscription(ctx, &pubsubpb.GetSubscriptionRequest{
		Subscription: subscriptionName,
	})
	if status.Code(err) == codes.NotFound {
		topicName := fmt.Sprintf("projects/%s/topics/%s", conf.Project, conf.Topic)
		_, err = client.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{
			Name:  subscriptionName,
			Topic: topicName,
		})
		if status.Code(err) == codes.AlreadyExists {
			err = nil
		}
	}
	if err != nil {
		return err
	}

	receiveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var callbackErr error
	var failOnce sync.Once
	fail := func(err error) {
		if err == nil {
			return
		}
		failOnce.Do(func() {
			callbackErr = err
			cancel()
		})
	}

	err = client.Subscriber(subscriptionName).Receive(receiveCtx, func(messageCtx context.Context, m *pubsub.Message) {
		ev := &Event{}
		if err := Unmarshal(m.Data, ev); err != nil {
			m.Nack()
			fail(fmt.Errorf("unmarshal Pub/Sub event: %w", err))
			return
		}
		if err := w.WriteEvent(messageCtx, ev); err != nil {
			m.Nack()
			if ctx.Err() == nil {
				fail(fmt.Errorf("write Pub/Sub event: %w", err))
			}
			return
		}

		ackStatus, err := m.AckWithResult().Get(messageCtx)
		if err != nil {
			if ctx.Err() == nil {
				fail(fmt.Errorf("acknowledge Pub/Sub event: %w", err))
			}
			return
		}
		if ackStatus != pubsub.AcknowledgeStatusSuccess {
			fail(fmt.Errorf("acknowledge Pub/Sub event: status %v", ackStatus))
		}
	})
	if callbackErr != nil {
		return callbackErr
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
