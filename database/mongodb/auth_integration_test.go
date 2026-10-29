package mongodb

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/calypr/funnel/config"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestMongoDBAuthentication(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "mongo:7", ExposedPorts: []string{"27017/tcp"},
			Env:        map[string]string{"MONGO_INITDB_ROOT_USERNAME": "root", "MONGO_INITDB_ROOT_PASSWORD": "test-root-password"},
			WaitingFor: wait.ForLog("Waiting for connections").WithOccurrence(2).WithStartupTimeout(90 * time.Second),
		}, Started: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Error(err)
		}
	})
	code, out, err := container.Exec(ctx, []string{"mongosh", "--quiet", "--username", "root", "--password", "test-root-password", "--authenticationDatabase", "admin", "--eval", `db.getSiblingDB("funnel").createUser({user:"funnel-user",pwd:"test@password:1",roles:[{role:"readWrite",db:"funnel"}]})`})
	if err != nil {
		t.Fatal(err)
	}
	message, err := io.ReadAll(out)
	if err != nil || code != 0 {
		t.Fatalf("create user: exit %d: %s: %v", code, message, err)
	}
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "27017/tcp")
	if err != nil {
		t.Fatal(err)
	}
	address := net.JoinHostPort(host, port.Port())
	for _, tc := range []struct {
		name, uri, user, password string
		wantError                 bool
	}{
		{"separate credentials", "mongodb://" + address + "/?authSource=funnel&directConnection=true", "funnel-user", "test@password:1", false},
		{"URI database as auth source", "mongodb://" + address + "/funnel?directConnection=true", "funnel-user", "test@password:1", false},
		{"URI credentials", "mongodb://" + url.UserPassword("funnel-user", "test@password:1").String() + "@" + address + "/?authSource=funnel&directConnection=true", "", "", false},
		{"separate credentials override URI credentials", "mongodb://wrong:wrong@" + address + "/?authSource=funnel&directConnection=true", "funnel-user", "test@password:1", false},
		{"host with separate admin credentials", address, "root", "test-root-password", false},
		{"URI with separate admin credentials", "mongodb://" + address, "root", "test-root-password", false},
		{"invalid password", "mongodb://" + address + "/?authSource=funnel&directConnection=true", "funnel-user", "wrong", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conf := &config.MongoDB{Addrs: []string{tc.uri}, Database: "funnel", Username: tc.user, Password: tc.password, Timeout: &config.TimeoutConfig{TimeoutOption: &config.TimeoutConfig_Duration{Duration: durationpb.New(5 * time.Second)}}}
			db, err := NewMongoDB(conf)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			pingCtx, done := context.WithTimeout(ctx, 10*time.Second)
			defer done()
			err = db.client.Ping(pingCtx, nil)
			if tc.wantError && err == nil {
				t.Fatal("authentication unexpectedly succeeded")
			}
			if !tc.wantError && err != nil {
				t.Fatal(fmt.Errorf("authenticate: %w", err))
			}
		})
	}
}
