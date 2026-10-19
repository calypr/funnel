package storage

import (
	"context"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/tests"
	"github.com/ohsu-comp-bio/funnel/worker"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	gs "google.golang.org/api/storage/v1"
)

func TestGoogleStorage(t *testing.T) {
	tests.SetLogOutput(log, t)
	defer os.RemoveAll("./test_tmp")

	if !conf.GoogleStorage.Valid() {
		t.Skipf("Skipping google storage e2e tests...")
	}

	args := flag.Args()
	var projectID string
	if len(args) > 0 {
		projectID = args[0]
	}

	if projectID == "" {
		t.Fatal("Must provide projectID as an arg")
	}

	ev := events.NewTaskWriter("test-task", 0, &events.Logger{Log: log})
	testBucket := "funnel-e2e-tests-" + tests.RandomString(6)
	ctx := context.Background()
	parallelXfer := 10

	client, err := newGsTest()
	if err != nil {
		t.Fatal(err)
	}
	err = client.createBucket(projectID, testBucket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		client.deleteBucket(testBucket)
	}()

	protocol := "gs://"

	store, err := storage.NewMux(conf)
	if err != nil {
		t.Fatal("error configuring storage:", err)
	}

	fPath := "testdata/test_in"
	inFileURL := protocol + testBucket + "/" + fPath
	_, err = worker.UploadOutputs(ctx, []*tes.Output{
		{Url: inFileURL, Path: fPath},
	}, store, ev, parallelXfer, "")
	if err != nil {
		t.Fatal("error uploading test file:", err)
	}

	dPath := "testdata/test_dir"
	inDirURL := protocol + testBucket + "/" + dPath
	_, err = worker.UploadOutputs(ctx, []*tes.Output{
		{Url: inDirURL, Path: dPath, Type: tes.Directory},
	}, store, ev, parallelXfer, "")
	if err != nil {
		t.Fatal("error uploading test directory:", err)
	}

	outFileURL := protocol + testBucket + "/" + "test-output-file.txt"
	outDirURL := protocol + testBucket + "/" + "test-output-directory"

	task := &tes.Task{
		Name: "storage e2e",
		Inputs: []*tes.Input{
			{
				Url:  inFileURL,
				Path: "/opt/inputs/test-file.txt",
				Type: tes.FileType_FILE,
			},
			{
				Url:  inDirURL,
				Path: "/opt/inputs/test-directory",
				Type: tes.FileType_DIRECTORY,
			},
		},
		Outputs: []*tes.Output{
			{
				Path: "/opt/workdir/test-output-file.txt",
				Url:  outFileURL,
				Type: tes.FileType_FILE,
			},
			{
				Path: "/opt/workdir/test-output-directory",
				Url:  outDirURL,
				Type: tes.FileType_DIRECTORY,
			},
		},
		Executors: []*tes.Executor{
			{
				Image: "alpine:latest",
				Command: []string{
					"sh",
					"-c",
					"cat $(find /opt/inputs -type f | sort) > test-output-file.txt; mkdir test-output-directory; cp *.txt test-output-directory/",
				},
				Workdir: "/opt/workdir",
			},
		},
	}

	resp, err := fun.RPC.CreateTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}

	taskFinal := fun.Wait(resp.Id)

	if taskFinal.State != tes.State_COMPLETE {
		t.Fatal("Unexpected task failure")
	}

	expected := "file1 content\nfile2 content\nhello\n"

	err = worker.DownloadInputs(ctx, []*tes.Input{
		{Url: outFileURL, Path: "./test_tmp/test-gs-file.txt"},
	}, store, ev, parallelXfer)
	if err != nil {
		t.Fatal("Failed to download file:", err)
	}

	b, err := os.ReadFile("./test_tmp/test-gs-file.txt")
	if err != nil {
		t.Fatal("Failed to read downloaded file:", err)
	}
	actual := string(b)

	if actual != expected {
		t.Log("expected:", expected)
		t.Log("actual:  ", actual)
		t.Fatal("unexpected content")
	}

	err = worker.DownloadInputs(ctx, []*tes.Input{
		{Url: outDirURL, Path: "./test_tmp/test-gs-directory", Type: tes.Directory},
	}, store, ev, parallelXfer)
	if err != nil {
		t.Fatal("Failed to download directory:", err)
	}

	b, err = os.ReadFile("./test_tmp/test-gs-directory/test-output-file.txt")
	if err != nil {
		t.Fatal("Failed to read file in downloaded directory", err)
	}
	actual = string(b)

	if actual != expected {
		t.Log("expected:", expected)
		t.Log("actual:  ", actual)
		t.Fatal("unexpected content")
	}

	// should succeed with warning when there is an input or output directory that
	// does not exist
	task = &tes.Task{
		Name: "storage e2e",
		Inputs: []*tes.Input{
			{
				Url:  protocol + testBucket + "/this/path/does/not/exist",
				Path: "/opt/inputs/test-directory",
				Type: tes.FileType_DIRECTORY,
			},
		},
		Outputs: []*tes.Output{
			{
				Path: "/opt/workdir/this/path/does/not/exist/test-output-directory",
				Url:  outDirURL,
				Type: tes.FileType_DIRECTORY,
			},
		},
		Executors: []*tes.Executor{
			{
				Image: "alpine:latest",
				Command: []string{
					"sleep", "1",
				},
			},
		},
	}

	resp, err = fun.RPC.CreateTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}

	taskFinal = fun.Wait(resp.Id)
	if taskFinal.State != tes.State_COMPLETE {
		t.Fatal("Expected task failure")
	}
	found := false
	for _, log := range taskFinal.Logs[0].SystemLogs {
		if strings.Contains(log, "level='warning'") {
			found = true
		}
	}
	if !found {
		t.Fatal("Expected warning in system logs")
	}
}

type gsTest struct {
	client *gs.Service
}

func newGsTest() (*gsTest, error) {
	defClient, err := google.DefaultClient(context.Background(), gs.CloudPlatformScope)
	if err != nil {
		return nil, err
	}
	client, err := gs.NewService(context.TODO(), option.WithHTTPClient(defClient))
	if err != nil {
		return nil, err
	}
	return &gsTest{client}, err
}

func (g *gsTest) createBucket(projectID, bucket string) error {
	req := g.client.Buckets.Insert(projectID, &gs.Bucket{
		Name: bucket,
	})
	_, err := req.Do()
	return err
}

func (g *gsTest) deleteBucket(bucket string) error {
	objects, err := g.client.Objects.List(bucket).Do()
	if err != nil {
		return err
	}
	for _, obj := range objects.Items {
		err = g.client.Objects.Delete(bucket, obj.Name).Do()
		if err != nil {
			return err
		}
	}
	return g.client.Buckets.Delete(bucket).Do()
}
