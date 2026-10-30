package events

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/v2/pstest"
	"github.com/calypr/funnel/config"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type pubSubTestWriter struct {
	events chan *Event
	err    error
}

func (w *pubSubTestWriter) WriteEvent(_ context.Context, ev *Event) error {
	if w.err != nil {
		return w.err
	}
	w.events <- ev
	return nil
}

func (*pubSubTestWriter) Close() {}

func startPubSubTestServer(t *testing.T, opts ...pstest.ServerReactorOption) *pstest.Server {
	t.Helper()
	server := pstest.NewServer(opts...)
	t.Setenv("PUBSUB_EMULATOR_HOST", server.Addr)
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close Pub/Sub test server: %v", err)
		}
	})
	return server
}

func waitForPubSubSubscription(t *testing.T, project, subname, topicName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := pubsub.NewClient(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Pub/Sub test client: %v", err)
		}
	}()

	name := "projects/" + project + "/subscriptions/" + subname
	for {
		sub, err := client.SubscriptionAdminClient.GetSubscription(ctx, &pubsubpb.GetSubscriptionRequest{
			Subscription: name,
		})
		if err == nil {
			if sub.Topic != topicName {
				t.Fatalf("subscription topic = %q, want %q", sub.Topic, topicName)
			}
			return
		}
		if status.Code(err) != codes.NotFound {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for Pub/Sub subscription creation")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForPubSubAck(t *testing.T, server *pstest.Server, id string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if msg := server.Message(id); msg != nil && msg.Acks > 0 {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for Pub/Sub acknowledgement")
		case <-ticker.C:
		}
	}
}

func TestPubSubWriterAndReader(t *testing.T) {
	server := startPubSubTestServer(t)
	conf := &config.PubSub{Project: "funnel-test", Topic: "events"}
	topicName := "projects/" + conf.Project + "/topics/" + conf.Topic
	subname := "events-reader"

	writerCtx, cancelWriter := context.WithCancel(context.Background())
	defer cancelWriter()
	writer, err := NewPubSubWriter(writerCtx, conf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)

	readerCtx, cancelReader := context.WithCancel(context.Background())
	defer cancelReader()
	readWriter := &pubSubTestWriter{events: make(chan *Event, 1)}
	readDone := make(chan error, 1)
	go func() {
		readDone <- ReadPubSub(readerCtx, conf, subname, readWriter)
	}()
	waitForPubSubSubscription(t, conf.Project, subname, topicName)

	if err := writer.WriteEvent(writerCtx, &Event{Type: Type_EXECUTOR_STDOUT}); err != nil {
		t.Fatal(err)
	}
	if got := len(server.Messages()); got != 0 {
		t.Fatalf("stdout event was published; got %d messages", got)
	}

	want := &Event{
		Id:        "event-1",
		Timestamp: "2026-10-15T12:00:00Z",
		Type:      Type_TASK_START_TIME,
		Data:      &Event_StartTime{StartTime: "2026-10-15T12:00:00Z"},
	}
	if err := writer.WriteEvent(writerCtx, want); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-readWriter.events:
		if !proto.Equal(got, want) {
			t.Fatalf("ReadPubSub delivered %v, want %v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ReadPubSub to deliver the event")
	}

	wantData, err := Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	messages := server.Messages()
	if len(messages) != 1 {
		t.Fatalf("got %d published messages, want 1", len(messages))
	}
	if messages[0].Topic != topicName {
		t.Fatalf("published to %q, want %q", messages[0].Topic, topicName)
	}
	if string(messages[0].Data) != wantData {
		t.Fatalf("published payload %q, want %q", messages[0].Data, wantData)
	}
	waitForPubSubAck(t, server, messages[0].ID)

	cancelReader()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("ReadPubSub returned after cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadPubSub did not stop after cancellation")
	}
}

type blockingPublishReactor struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingPublishReactor) React(interface{}) (bool, interface{}, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return true, nil, status.Error(codes.PermissionDenied, "publish failed")
}

func TestPubSubWriterEnqueuesWithoutWaitingForPublishResult(t *testing.T) {
	reactor := &blockingPublishReactor{started: make(chan struct{}), release: make(chan struct{})}
	startPubSubTestServer(t, pstest.ServerReactorOption{FuncName: "Publish", Reactor: reactor})
	conf := &config.PubSub{Project: "funnel-test", Topic: "events"}
	writer, err := NewPubSubWriter(context.Background(), conf)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(reactor.release)
		writer.Close()
	}()

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writer.WriteEvent(context.Background(), &Event{Id: "event-1", Type: Type_TASK_START_TIME})
	}()

	select {
	case <-reactor.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Pub/Sub did not begin the queued publish")
	}
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("WriteEvent() error = %v, want successful enqueue", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WriteEvent waited for the remote publish result")
	}
}

func TestReadPubSubReturnsAckError(t *testing.T) {
	server := startPubSubTestServer(t, pstest.WithErrorInjection("Acknowledge", codes.PermissionDenied, "ack failed"))
	conf := &config.PubSub{Project: "funnel-test", Topic: "events"}
	topicName := "projects/" + conf.Project + "/topics/" + conf.Topic
	subscriptionName := "projects/" + conf.Project + "/subscriptions/events-reader"

	client, err := pubsub.NewClient(context.Background(), conf.Project)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.TopicAdminClient.CreateTopic(context.Background(), &pubsubpb.Topic{Name: topicName})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SubscriptionAdminClient.CreateSubscription(context.Background(), &pubsubpb.Subscription{
		Name:                      subscriptionName,
		Topic:                     topicName,
		EnableExactlyOnceDelivery: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	want := &Event{Id: "event-1", Type: Type_TASK_START_TIME}
	data, err := Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	server.Publish(topicName, []byte(data), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readWriter := &pubSubTestWriter{events: make(chan *Event, 1)}
	readDone := make(chan error, 1)
	go func() {
		readDone <- ReadPubSub(ctx, conf, "events-reader", readWriter)
	}()

	select {
	case got := <-readWriter.events:
		if !proto.Equal(got, want) {
			t.Fatalf("ReadPubSub delivered %v, want %v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ReadPubSub to deliver the event")
	}

	select {
	case err := <-readDone:
		if err == nil || !strings.Contains(err.Error(), "ack failed") {
			t.Fatalf("ReadPubSub() error = %v, want acknowledgement failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadPubSub did not return the acknowledgement failure")
	}
}

func TestReadPubSubReturnsWriterErrorWithoutAcknowledging(t *testing.T) {
	server := startPubSubTestServer(t)
	conf := &config.PubSub{Project: "funnel-test", Topic: "events"}
	topicName := "projects/" + conf.Project + "/topics/" + conf.Topic
	subscriptionName := "projects/" + conf.Project + "/subscriptions/events-reader"

	client, err := pubsub.NewClient(context.Background(), conf.Project)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.TopicAdminClient.CreateTopic(context.Background(), &pubsubpb.Topic{Name: topicName})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SubscriptionAdminClient.CreateSubscription(context.Background(), &pubsubpb.Subscription{
		Name:  subscriptionName,
		Topic: topicName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	want := &Event{Id: "event-1", Type: Type_TASK_START_TIME}
	data, err := Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	id := server.Publish(topicName, []byte(data), nil)

	writeErr := errors.New("writer failed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readDone := make(chan error, 1)
	go func() {
		readDone <- ReadPubSub(ctx, conf, "events-reader", &pubSubTestWriter{err: writeErr})
	}()

	select {
	case err := <-readDone:
		if !errors.Is(err, writeErr) {
			t.Fatalf("ReadPubSub() error = %v, want %v", err, writeErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadPubSub did not return the writer error")
	}
	msg := server.Message(id)
	if msg == nil || msg.Acks != 0 {
		t.Fatalf("message ack count = %v, want 0", msg)
	}
	for _, modack := range msg.Modacks {
		if modack.AckDeadline == 0 {
			return
		}
	}
	t.Fatalf("message modacks = %v, want an immediate nack", msg.Modacks)
}
