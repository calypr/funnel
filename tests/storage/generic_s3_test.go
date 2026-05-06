package storage

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/minio/minio-go"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/tests"
	"github.com/ohsu-comp-bio/funnel/worker"
)

func TestGenericS3Storage(t *testing.T) {
	conf = tests.DefaultConfig()
	tests.SetLogOutput(log, t)
	defer os.RemoveAll("./test_tmp")

	if len(conf.GenericS3) > 0 {
		if !conf.GenericS3[0].Valid() {
			t.Skipf("Skipping generic s3 e2e tests...")
		}
	} else {
		t.Skipf("Skipping generic s3 e2e tests...")
	}

	ev := events.NewTaskWriter("test-task", 0, &events.Logger{Log: log})
	testBucket := "funnel-e2e-tests-" + tests.RandomString(6)
	ctx := context.Background()
	parallelXfer := 10

	client, err := newMinioTest(conf.GenericS3[0])
	if err != nil {
		t.Fatal("error creating minio client:", err)
	}
	err = client.createBucket(testBucket)
	if err != nil {
		t.Fatal("error creating test bucket:", err)
	}
	defer func() {
		err = client.deleteBucket(testBucket)
		if err != nil {
			t.Fatal("error deleting test bucket:", err)
		}
	}()

	protocol := "s3://"

	store, err := storage.NewMux(conf)
	if err != nil {
		t.Fatal("error configuring storage:", err)
	}

	fPath := "testdata/test_in"
	inFileURL := protocol + testBucket + "/" + fPath
	_, err = worker.UploadOutputs(ctx, []*tes.Output{
		{Url: inFileURL, Path: fPath},
	}, store, ev, parallelXfer)
	if err != nil {
		t.Fatal("error uploading test file:", err)
	}

	dPath := "testdata/test_dir"
	inDirURL := protocol + testBucket + "/" + dPath
	_, err = worker.UploadOutputs(ctx, []*tes.Output{
		{Url: inDirURL, Path: dPath},
	}, store, ev, parallelXfer)
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
			},
			{
				Url:  inDirURL,
				Path: "/opt/inputs/test-directory",
			},
		},
		Outputs: []*tes.Output{
			{
				Path: "/opt/workdir/test-output-file.txt",
				Url:  outFileURL,
			},
			{
				Path: "/opt/workdir/test-output-directory",
				Url:  outDirURL,
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
		{Url: outFileURL, Path: "./test_tmp/test-s3-file.txt"},
	}, store, ev, parallelXfer)
	if err != nil {
		t.Fatal("Failed to download file:", err)
	}

	b, err := os.ReadFile("./test_tmp/test-s3-file.txt")
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
		{Url: outDirURL, Path: "./test_tmp/test-s3-directory"},
	}, store, ev, parallelXfer)
	if err != nil {
		t.Fatal("Failed to download directory:", err)
	}

	b, err = os.ReadFile("./test_tmp/test-s3-directory/test-output-directory/test-output-file.txt")
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
			},
		},
		Outputs: []*tes.Output{
			{
				Path: "/opt/workdir/this/path/does/not/exist/test-output-directory",
				Url:  outDirURL,
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
	if taskFinal.State != tes.State_SYSTEM_ERROR {
		t.Fatal("Expected task failure")
	}
	found := false
	for _, log := range taskFinal.Logs[0].SystemLogs {
		if strings.Contains(log, "level='error'") {
			found = true
		}
	}
	if !found {
		t.Fatal("Expected error in system logs")
	}
}

type minioTest struct {
	client *minio.Client
	fcli   *storage.GenericS3
}

func newMinioTest(conf *config.GenericS3Storage) (*minioTest, error) {
	ssl := strings.HasPrefix(conf.Endpoint, "https")
	client, err := minio.NewV2(conf.Endpoint, conf.Key, conf.Secret, ssl)
	if err != nil {
		return nil, err
	}

	fcli, err := storage.NewGenericS3(conf)
	if err != nil {
		return nil, err
	}

	return &minioTest{client, fcli}, nil
}

func (b *minioTest) createBucket(bucket string) error {
	return b.client.MakeBucket(bucket, "")
}

func (b *minioTest) deleteBucket(bucket string) error {
	doneCh := make(chan struct{})
	defer close(doneCh)
	recursive := true
	for obj := range b.client.ListObjects(bucket, "", recursive, doneCh) {
		err := b.client.RemoveObject(bucket, obj.Key)
		if err != nil {
			return err
		}
	}
	return b.client.RemoveBucket(bucket)
}
