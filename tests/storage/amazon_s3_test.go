package storage

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/tests"
	util "github.com/ohsu-comp-bio/funnel/util/aws"
	"github.com/ohsu-comp-bio/funnel/worker"
)

func TestAmazonS3Storage(t *testing.T) {
	tests.SetLogOutput(log, t)
	defer os.RemoveAll("./test_tmp")

	if !conf.AmazonS3.Valid() {
		t.Skipf("Skipping amazon s3 e2e tests...")
	}

	ev := events.NewTaskWriter("test-task", 0, &events.Logger{Log: log})
	testBucket := "funnel-e2e-tests-" + tests.RandomString(6)
	ctx := context.Background()
	parallelXfer := 10

	client, err := newS3Test(conf.AmazonS3)
	if err != nil {
		t.Fatal("error creating minio client:", err)
	}
	err = client.createBucket(testBucket)
	if err != nil {
		t.Fatal("error creating test bucket:", err)
	}
	defer func() {
		client.deleteBucket(testBucket)
	}()

	protocol := "s3://"

	store, err := storage.NewMux(conf)
	if err != nil {
		t.Fatal("error configuring storage:", err)
	}

	// Upload single file
	fPath := "testdata/test_in"
	inFileURL := protocol + testBucket + "/" + fPath
	_, err = worker.UploadOutputs(ctx, []*tes.Output{
		{Url: inFileURL, Path: fPath},
	}, store, ev, parallelXfer)
	if err != nil {
		t.Fatal("error uploading test file:", err)
	}

	// Upload directory
	dPath := "testdata/test_dir"
	inDirURL := protocol + testBucket + "/" + dPath
	_, err = worker.UploadOutputs(ctx, []*tes.Output{
		{Url: inDirURL, Path: dPath, Type: tes.Directory},
	}, store, ev, parallelXfer)
	if err != nil {
		t.Fatal("error uploading test directory:", err)
	}

	// Expected task output paths
	outFileURL := protocol + testBucket + "/" + "test-output-file.txt"
	outDirURL := protocol + testBucket + "/" + "test-output-directory"

	// Task definition which will test downloading/uploading the inputs/outputs.
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

	// Run the task
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
		{Url: outDirURL, Path: "./test_tmp/test-s3-directory", Type: tes.Directory},
	}, store, ev, parallelXfer)
	if err != nil {
		t.Fatal("Failed to download directory:", err)
	}

	b, err = os.ReadFile("./test_tmp/test-s3-directory/test-output-file.txt")
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

type s3Test struct {
	client *s3.S3
}

func newS3Test(conf *config.AmazonS3Storage) (*s3Test, error) {
	sess, err := util.NewAWSSession(conf.AWSConfig)
	if err != nil {
		return nil, err
	}
	client := s3.New(sess)
	return &s3Test{client}, nil
}

func (b *s3Test) createBucket(bucket string) error {
	_, err := b.client.CreateBucket(&s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	return err
}

func (b *s3Test) deleteBucket(bucket string) error {
	params := &s3.ListObjectsInput{
		Bucket: aws.String(bucket),
	}
	for {
		//Requesting for batch of objects from s3 bucket
		objects, err := b.client.ListObjects(params)
		if err != nil {
			return err
		}
		//Checks if the bucket is already empty
		if len((*objects).Contents) == 0 {
			log.Info("Bucket is already empty")
			return nil
		}

		//creating an array of pointers of ObjectIdentifier
		objectsToDelete := make([]*s3.ObjectIdentifier, 0, 1000)
		for _, object := range (*objects).Contents {
			obj := s3.ObjectIdentifier{
				Key: object.Key,
			}
			objectsToDelete = append(objectsToDelete, &obj)
		}
		//Creating JSON payload for bulk delete
		deleteArray := s3.Delete{Objects: objectsToDelete}
		deleteParams := &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &deleteArray,
		}
		//Running the Bulk delete job (limit 1000)
		_, err = b.client.DeleteObjects(deleteParams)
		if err != nil {
			return err
		}
		if *(*objects).IsTruncated { //if there are more objects in the bucket, IsTruncated = true
			params.Marker = (*deleteParams).Delete.Objects[len((*deleteParams).Delete.Objects)-1].Key
		} else { //if all objects in the bucket have been cleaned up.
			break
		}
	}
	_, err := b.client.DeleteBucket(&s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	return err
}
