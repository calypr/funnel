package events

import (
	"log"
	"sync"

	"context"

	"github.com/IBM/sarama"
	"github.com/calypr/funnel/api/config"
)

// KafkaWriter writes events to a Kafka topic.
type KafkaWriter struct {
	conf      *config.Kafka
	producer  sarama.SyncProducer
	shutdown  chan struct{}
	closeOnce sync.Once
}

// NewKafkaWriter creates a new event writer for writing events to a Kafka topic.
func NewKafkaWriter(ctx context.Context, conf *config.Kafka) (*KafkaWriter, error) {
	producer, err := sarama.NewSyncProducer(conf.Servers, nil)
	if err != nil {
		return nil, err
	}
	writer := &KafkaWriter{conf: conf, producer: producer, shutdown: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			writer.Close()
		case <-writer.shutdown:
		}
	}()
	return writer, nil
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
	k.closeOnce.Do(func() {
		close(k.shutdown)
		if err := k.producer.Close(); err != nil {
			log.Printf("close kafka writer: %v", err)
		}
	})
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

	// This reader consumes only partition 0 from the newest offset; multi-partition
	// topics need consumer-group rebalancing before they can be used here.
	p, err := con.ConsumePartition(conf.Topic, 0, sarama.OffsetNewest)
	if err != nil {
		return nil, err
	}

	go func() {
		for msg := range p.Messages() {
			ev := &Event{}
			err := Unmarshal(msg.Value, ev)
			if err != nil {
				log.Printf("kafka: dropping malformed event message at offset %d: %v", msg.Offset, err)
				continue
			}
			err = w.WriteEvent(context.Background(), ev)
			if err != nil {
				log.Printf("kafka: dropping event at offset %d after write failure: %v", msg.Offset, err)
			}
		}
	}()

	go func() {
		<-ctx.Done()
		err := con.Close()
		if err != nil {
			log.Printf("close kafka consumer: %v", err)
		}
		err = p.Close()
		if err != nil {
			log.Printf("close kafka partition consumer: %v", err)
		}
	}()
	return &KafkaReader{conf, con, p}, nil
}
