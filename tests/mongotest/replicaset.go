// Package mongotest provides a real single-node replica set for database tests.
package mongotest

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func Start(t *testing.T) string {
	t.Helper()
	return start(t, true)
}

func StartStandalone(t *testing.T) string {
	t.Helper()
	return start(t, false)
}

func start(t *testing.T, replica bool) string {
	t.Helper()
	if testing.Short() {
		t.Skip("MongoDB integration test requires Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := []string{"--bind_ip_all"}
	if replica {
		command = append(command, "--replSet", "rs0")
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "mongo:7", ExposedPorts: []string{"27017/tcp"},
			Cmd:        command,
			WaitingFor: wait.ForLog("Waiting for connections").WithStartupTimeout(90 * time.Second),
		}, Started: true,
	})
	if err != nil {
		t.Fatalf("start MongoDB container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Errorf("terminate MongoDB: %v", err)
		}
	})
	if replica {
		code, output, err := container.Exec(ctx, []string{"mongosh", "--quiet", "--eval", `const r = rs.initiate({_id:"rs0",members:[{_id:0,host:"localhost:27017"}]}); if (!r.ok) { printjson(r); quit(1); }`})
		if err != nil {
			t.Fatalf("initialize MongoDB replica set: %v", err)
		}
		data, err := io.ReadAll(output)
		if err != nil || code != 0 {
			t.Fatalf("initialize MongoDB replica set: exit=%d output=%s error=%v", code, data, err)
		}
		ready := wait.ForExec([]string{"mongosh", "--quiet", "--eval", `quit(db.hello().isWritablePrimary ? 0 : 1)`}).WithStartupTimeout(60 * time.Second)
		if err := ready.WaitUntilReady(ctx, container); err != nil {
			t.Fatalf("wait for MongoDB primary: %v", err)
		}
	}
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "27017/tcp")
	if err != nil {
		t.Fatal(err)
	}
	// The replica set advertises its container-local address. Direct connection
	// keeps the test driver on the host-mapped port while retaining transactions.
	uri := "mongodb://" + net.JoinHostPort(host, port.Port()) + "/?directConnection=true"
	if replica {
		uri += "&replicaSet=rs0"
	}
	return uri
}
