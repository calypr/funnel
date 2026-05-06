package events

import (
	"context"

	"github.com/Shopify/sarama"
	"github.com/ohsu-comp-bio/funnel/config"
)

// KafkaWriter writes events to a Kafka topic.
type KafkaWriter struct {
	conf     *config.Kafka
	producer sarama.SyncProducer
}

// NewKafkaWriter creates a new event writer for writing events to a Kafka topic.
func NewKafkaWriter(ctx context.Context, conf *config.Kafka) (*KafkaWriter, error) {
	producer, err := sarama.NewSyncProducer(conf.Servers, nil)
	if err != nil {
		return nil, err
	}
	go func() {
		<-ctx.Done()
		producer.Close()
	}()
	return &KafkaWriter{conf, producer}, nil
}

// WriteEvent writes the event. Events may be sent in batches in the background by the
// Kafka client library. Currently stdout, stderr, and system log events are dropped.
func (k *KafkaWriter) WriteEvent(ctx context.Context, ev *Event) error {

	switch ev.Type {
	case Type_EXECUTOR_STDOUT, Type_EXECUTOR_STDERR, Type_SYSTEM_LOG:
		return nil
	}

	s, err := Marshal(ev)
	if err != nil {
		return err
	}

	msg := &sarama.ProducerMessage{
		Topic: k.conf.Topic,
		Key:   nil,
		Value: sarama.StringEncoder(s),
	}
	_, _, err = k.producer.SendMessage(msg)
	return err
}

func (k *KafkaWriter) Close() {
	k.producer.Close()
}

// KafkaReader reads events to a Kafka topic and writes them
// to a Writer.
type KafkaReader struct {
	conf *config.Kafka
	con  sarama.Consumer
	pcon sarama.PartitionConsumer
}

// NewKafkaReader creates a new event reader for reading events from a Kafka topic and writing them to the given Writer.
func NewKafkaReader(ctx context.Context, conf *config.Kafka, w Writer) (*KafkaReader, error) {
	con, err := sarama.NewConsumer(conf.Servers, nil)
	if err != nil {
		return nil, err
	}

	// TODO better handling of partition and offset.
	p, err := con.ConsumePartition(conf.Topic, 0, sarama.OffsetNewest)
	if err != nil {
		return nil, err
	}

	go func() {
		for msg := range p.Messages() {
			ev := &Event{}
			err := Unmarshal(msg.Value, ev)
			if err != nil {
				// TODO
				continue
			}
			w.WriteEvent(context.Background(), ev)
		}
	}()

	go func() {
		<-ctx.Done()
		con.Close()
		p.Close()
	}()
	return &KafkaReader{conf, con, p}, nil
}
