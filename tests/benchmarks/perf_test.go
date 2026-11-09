package benchmarks

import (
	"context"
	"sync"
	"testing"
	"time"

	appconfig "github.com/calypr/funnel/internal/config"

	"github.com/calypr/funnel/internal/events"
	"github.com/calypr/funnel/tests/internal/testenv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func BenchmarkRunSerialNoNodes(b *testing.B) {
	conf := testenv.DefaultConfig(b)
	// No nodes connected in this test
	conf.Compute = "manual"
	conf.Logger.Level = "error"
	fun := testenv.NewFunnel(b, conf)

	fun.StartServer()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		fun.Run(`
      --sh 'echo'
    `)
	}
}

func BenchmarkRunConcurrentNoNodes(b *testing.B) {
	conf := testenv.DefaultConfig(b)
	// No nodes connected in this test
	conf.Compute = "manual"
	conf.Logger.Level = "error"
	fun := testenv.NewFunnel(b, conf)
	fun.StartServer()
	b.ResetTimer()

	var wg sync.WaitGroup

	for i := 0; i < b.N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fun.Run(`
        --sh 'echo'
      `)
		}()
	}
	wg.Wait()
}

func BenchmarkRunConcurrentWithFakeNodes(b *testing.B) {
	conf := testenv.DefaultConfig(b)
	// Nodes are simulated by goroutines writing to the scheduler API
	conf.Compute = "manual"
	conf.Logger.Level = "error"
	fun := testenv.NewFunnel(b, conf)
	fun.StartServer()

	var wg sync.WaitGroup
	ids := make(chan string, 1000)
	done := make(chan struct{})
	var fakeNodes sync.WaitGroup
	defer func() { close(done); fakeNodes.Wait() }()

	// Generate a 1000 character string to write as stdout logs
	content := ""
	for j := 0; j < 1000; j++ {
		content += "a"
	}

	// When a task is created, start a fake node that writes to the database.
	fakeNodes.Add(1)
	go func() {
		defer fakeNodes.Done()
		for {
			select {
			case id := <-ids:
				// fake node that writes to UpdateExecutorLogs every tick
				fakeNodes.Add(1)
				go func(id string) {
					defer fakeNodes.Done()
					conn, err := grpc.Dial(appconfig.RPCAddress(fun.Conf.Server), grpc.WithTransportCredentials(insecure.NewCredentials()))
					if err != nil {
						panic(err)
					}
					defer func() {
						if err := conn.Close(); err != nil {
							b.Errorf("close fake node: %v", err)
						}
					}()
					cli := events.NewEventServiceClient(conn)
					ticker := time.NewTicker(time.Millisecond * 20)
					defer ticker.Stop()

					for {
						select {
						case <-ticker.C:
							_, err := cli.WriteEvent(context.Background(), &events.Event{
								Id:      id,
								Attempt: 0,
								Index:   0,
								Type:    events.Type_EXECUTOR_STDOUT,
								Data: &events.Event_Stdout{
									Stdout: content,
								},
								Timestamp: time.Now().Format(time.RFC3339Nano),
							})
							if err != nil {
								b.Errorf("write benchmark executor event: %v", err)
								return
							}
						case <-done:
							return
						}
					}
				}(id)
			case <-done:
				return
			}
		}
	}()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids <- fun.Run(`
        --sh 'echo'
      `)
		}()
	}

	wg.Wait()
}
