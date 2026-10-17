package perf

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/calypr/funnel/events"
	"github.com/calypr/funnel/tests"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func BenchmarkRunSerialNoNodes(b *testing.B) {
	fun := tests.NewFunnel(tests.DefaultConfig())
	defer fun.Cleanup()
	// No nodes connected in this test
	fun.Conf.Compute = "manual"
	fun.Conf.Logger.Level = "error"

	fun.StartServer()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		fun.Run(`
      --sh 'echo'
    `)
	}
}

func BenchmarkRunConcurrentNoNodes(b *testing.B) {
	fun := tests.NewFunnel(tests.DefaultConfig())
	defer fun.Cleanup()
	// No nodes connected in this test
	fun.Conf.Compute = "manual"
	fun.Conf.Logger.Level = "error"
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
	fun := tests.NewFunnel(tests.DefaultConfig())
	defer fun.Cleanup()
	// Nodes are simulated by goroutines writing to the scheduler API
	fun.Conf.Compute = "manual"
	fun.Conf.Logger.Level = "error"
	fun.StartServer()

	var wg sync.WaitGroup
	ids := make(chan string, 1000)
	done := make(chan struct{})
	defer close(done)

	// Generate a 1000 character string to write as stdout logs
	content := ""
	for j := 0; j < 1000; j++ {
		content += "a"
	}

	// When a task is created, start a fake node that writes to the database.
	go func() {
		for {
			select {
			case id := <-ids:
				// fake node that writes to UpdateExecutorLogs every tick
				go func(id string) {
					conn, err := grpc.Dial(fun.Conf.Server.RPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
					if err != nil {
						panic(err)
					}
					cli := events.NewEventServiceClient(conn)
					_ = cli
					ticker := time.NewTicker(time.Millisecond * 20)

					for {
						select {
						case <-ticker.C:
							cli.WriteEvent(context.Background(), &events.Event{
								Id:      id,
								Attempt: 0,
								Index:   0,
								Type:    events.Type_EXECUTOR_STDOUT,
								Data: &events.Event_Stdout{
									Stdout: content,
								},
								Timestamp: time.Now().Format(time.RFC3339Nano),
							})
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
