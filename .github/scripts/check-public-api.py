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
        MODULE + "/internal/", MODULE + "/api/config", MODULE + "/api/logger",
        "google.golang.org/grpc", "github.com/grpc-ecosystem/", "k8s.io/",
        "cloud.google.com/", "github.com/aws/",
    ))]
    if forbidden:
        raise RuntimeError("TES client imports server dependencies: " + ", ".join(forbidden))
    model_dependencies = run("go", "list", "-deps", "./api/tes").splitlines()
    if any(name == "net/http" or name.startswith(MODULE + "/client/") for name in model_dependencies):
        raise RuntimeError("TES wire models import HTTP/client behavior")
    for schema_package in ("api/config", "api/logger"):
        dependencies = run("go", "list", "-deps", "./" + schema_package).splitlines()
        forbidden = [name for name in dependencies if name.startswith((
            MODULE + "/internal/", "k8s.io/", "cloud.google.com/", "github.com/aws/",
            "github.com/go-playground/validator", "github.com/goccy/go-yaml",
        ))]
        direct_imports = json.loads(run("go", "list", "-json", "./" + schema_package)).get("Imports", [])
        forbidden += [name for name in direct_imports if name in {"os", "embed", "io/fs"}]
        if forbidden:
            raise RuntimeError(schema_package + " schemas import application behavior: " + ", ".join(forbidden))
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
 "bytes"
 "encoding/gob"
 "io"
 "net/http"
 "net/http/httptest"
 "testing"
 "github.com/calypr/funnel/api/tes"
 tesclient "github.com/calypr/funnel/client/tes"
 "github.com/calypr/funnel/api/config"
 "github.com/calypr/funnel/api/logger"
 plugin "github.com/calypr/funnel/api/plugin"
 "github.com/calypr/funnel/plugins/shared"
 "google.golang.org/protobuf/proto"
 "google.golang.org/protobuf/types/known/structpb"
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
func TestExternalPluginGobContract(t *testing.T) {
 volume,err:=structpb.NewStruct(map[string]any{"name":"data","nested":map[string]any{"value":"x"},"values":[]any{nil,true,float64(2),"x"}})
 if err!=nil {t.Fatal(err)}
 job:=&plugin.Job{Config:&config.Config{
  Logger:&logger.LoggerConfig{Level:"info"},
  GenericS3:[]*config.GenericS3Storage{{
   MountedRead:&config.S3MountedRead{Volume:"data",MountView:&config.S3MountedRead_SubPath{SubPath:"input"}},
   MountedWrite:&config.S3MountedWrite{Volume:"data",MountView:&config.S3MountedWrite_SubPath{SubPath:""}},
  }},
  Kubernetes:&config.Kubernetes{Storage:&config.KubernetesStorage{Volumes:[]*structpb.Struct{volume}}},
 },Task:&tes.Task{Id:"plugin-task"}}
 var buffer bytes.Buffer
 if err:=gob.NewEncoder(&buffer).Encode(job);err!=nil {t.Fatal(err)}
 decoded:=&plugin.Job{}
 if err:=gob.NewDecoder(&buffer).Decode(decoded);err!=nil {t.Fatal(err)}
 if !proto.Equal(job,decoded) {t.Fatal("plugin Gob codec changed values or oneof presence")}
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
    print("Public API check passed: HTTP-free wire models, schema-only config/logger dependencies, external client/plugin protobuf/Gob consumers, and enforced internal boundary.")


if __name__ == "__main__":
    main()
