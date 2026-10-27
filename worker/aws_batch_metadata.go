package worker

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/calypr/funnel/events"
)

func writeBatchInstanceType(ctx context.Context, event *events.TaskWriter) {
	if os.Getenv("AWS_BATCH_JOB_ID") == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	client := imds.New(imds.Options{EnableFallback: aws.FalseTernary})
	result, err := client.GetMetadata(ctx, &imds.GetMetadataInput{Path: "instance-type"})
	if err != nil {
		return
	}
	defer result.Content.Close()
	value, err := io.ReadAll(io.LimitReader(result.Content, 256))
	if err != nil {
		return
	}
	if instanceType := strings.TrimSpace(string(value)); instanceType != "" {
		_ = event.Metadata(map[string]string{"instance_type": instanceType})
	}
}
