#!/usr/bin/env python3
"""Verify that the TES REST client and retained plugin SDK work outside Funnel."""

import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
MODULE = "github.com/calypr/funnel"


def run(*args, cwd=ROOT):
    return subprocess.check_output(args, cwd=cwd, text=True, stderr=subprocess.STDOUT)


def main():
    dependencies = run("go", "list", "-deps", "./client/tes").splitlines()
    forbidden = [name for name in dependencies if name.startswith((
        MODULE + "/internal/", MODULE + "/config", MODULE + "/logger",
        "google.golang.org/grpc", "github.com/grpc-ecosystem/", "k8s.io/",
        "cloud.google.com/", "github.com/aws/",
    ))]
    if forbidden:
        raise RuntimeError("TES client imports server dependencies: " + ", ".join(forbidden))
    model_dependencies = run("go", "list", "-deps", "./tes").splitlines()
    if any(name == "net/http" or name.startswith(MODULE + "/client/") for name in model_dependencies):
        raise RuntimeError("TES wire models import HTTP/client behavior")
    env = os.environ | {"GOWORK": "off"}
    with tempfile.TemporaryDirectory(prefix="funnel-public-api-") as directory:
        path = Path(directory)
        path.joinpath("go.mod").write_text(
            "module example.com/funnel-consumer\n\ngo 1.27.3\n\n"
            f"require {MODULE} v0.0.0\nreplace {MODULE} => {json.dumps(str(ROOT))}\n"
        )
        path.joinpath("public_test.go").write_text('''package consumer_test
import (
 "context"
 "io"
 "net/http"
 "net/http/httptest"
 "testing"
 "github.com/calypr/funnel/tes"
 tesclient "github.com/calypr/funnel/client/tes"
 "github.com/calypr/funnel/config"
 "github.com/calypr/funnel/logger"
 plugin "github.com/calypr/funnel/plugins/proto"
 "github.com/calypr/funnel/plugins/shared"
 "google.golang.org/protobuf/proto"
)
func TestExternalClient(t *testing.T) {
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.URL.Path!="/prefix/v1/tasks" {t.Errorf("path = %s",r.URL.Path)}
  if _,err:=io.WriteString(w,`{"id":"external-task"}`);err!=nil {t.Error(err)}
 }));defer server.Close()
 client,err:=tesclient.NewClient(server.URL+"/prefix",tesclient.WithHTTPClient(server.Client()));if err!=nil {t.Fatal(err)}
 response,err:=client.CreateTask(context.Background(),&tes.Task{Executors:[]*tes.Executor{{Image:"alpine",Command:[]string{"true"}}}})
 if err!=nil {t.Fatal(err)};if response.Id!="external-task" {t.Fatal(response)}
}
type authorizer struct{}
func (authorizer) PluginAction(_ map[string]string, _ map[string]*plugin.StringList, conf *config.Config, task *tes.Task, _ plugin.Type) (*plugin.JobResponse,error) {
 return &plugin.JobResponse{Config:conf,Task:task},nil
}
var _ shared.Authorize = authorizer{}
func TestExternalPluginContracts(t *testing.T) {
 conf:=&config.Config{Logger:&logger.LoggerConfig{Level:"info"}}
 data,err:=proto.Marshal(conf);if err!=nil {t.Fatal(err)}
 decoded:=&config.Config{};if err:=proto.Unmarshal(data,decoded);err!=nil {t.Fatal(err)}
 if !proto.Equal(conf,decoded) {t.Fatal("plugin configuration changed during serialization")}
 _ = plugin.Type_CREATE
}
''')
        subprocess.run(["go", "mod", "tidy"], cwd=path, env=env, check=True)
        subprocess.run(["go", "test", "-count=1", "./..."], cwd=path, env=env, check=True)
        path.joinpath("private.go").write_text(
            'package consumer\nimport _ "github.com/calypr/funnel/internal/server"\n'
        )
        result = subprocess.run(["go", "build", "-mod=mod", "."], cwd=path, env=env,
                                text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if result.returncode == 0 or "use of internal package" not in result.stdout:
            raise RuntimeError("Expected Go's internal-package rejection, got:\n" + result.stdout)
    print("Public API check passed: HTTP-free wire models, REST-only client dependencies, external client/plugin consumers, and enforced internal boundary.")


if __name__ == "__main__":
    main()
