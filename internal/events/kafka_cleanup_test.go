package events

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/IBM/sarama"
)

type closeCountProducer struct {
	sarama.SyncProducer
	closes atomic.Int32
}

func (p *closeCountProducer) Close() error { p.closes.Add(1); return nil }

func TestKafkaWriterCloseIsIdempotent(t *testing.T) {
	producer := &closeCountProducer{}
	writer := &KafkaWriter{producer: producer, shutdown: make(chan struct{})}
	var calls sync.WaitGroup
	for range 16 {
		calls.Go(writer.Close)
	}
	calls.Wait()
	if got := producer.closes.Load(); got != 1 {
		t.Fatalf("producer closed %d times, want once", got)
	}
}
