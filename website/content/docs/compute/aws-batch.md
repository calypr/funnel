---
title: AWS Batch
menu:
  main:
    parent: Compute
    weight: 20
---

# AWS Batch

This guide covers deploying a Funnel server that leverages [DynamoDB][0] for storage
and [AWS Batch][1] for task execution.

## Setup

Get started by creating a compute environment, job queue and job definition using either
the Funnel CLI or the AWS Batch web console. To manage the permissions of instanced
AWS Batch jobs create a new IAM role. For the Funnel configuration outlined
in this document, this role will need to provide read and write access to both S3 and DynamoDB.

_Note_: We recommend creating the Job Definition with Funnel by running: `funnel aws batch create-job-definition`.
Funnel expects the JobDefinition to start a Funnel worker process with a specific configuration.
Only advanced users should consider making any substantial changes to this Job Definition.

AWS Batch tasks, by default, launch the ECS Optimized AMI which includes
an 8GB volume for the operating system and a 22GB volume for Docker image and metadata
storage. The default Docker configuration allocates up to 10GB of this storage to
each container instance. [Read more about the default AMI][8]. Due to these limitations, we
recommend [creating a custom AMI][7]. Because AWS Batch has the same requirements for your
AMI as Amazon ECS, use the default Amazon ECS-optimized Amazon Linux AMI as a base and change it
to better suit your tasks.

### Steps
* [Create a Compute Environment][3]
*  (_Optional_) [Create a custom AMI][7]
* [Create a Job Queue][4]
* [Create an EC2ContainerTaskRole with policies for managing access to S3 and DynamoDB][5]
* [Create a Job Definition][6]

For more information check out AWS Batch's [getting started guide][2].

### Quickstart

```
$ funnel aws batch create-all-resources --region us-west-2

```

This command will create a compute environment, job queue, IAM role and job definition.

### Pull private Amazon ECR images

To run a task with a private ECR image, configure registry authentication for
the Docker client used by the Funnel worker. The worker also needs AWS
credentials with permission to pull the image. See the AWS guide to
[private registry authentication](https://docs.aws.amazon.com/AmazonECR/latest/userguide/registry_auth.html),
including the ECR Docker credential helper.

### Local storage on AWS Batch

Local file paths must be accessible to the Batch worker. A file on the Funnel
server is not automatically available to workers on other machines. Use a
shared filesystem mounted on the workers and configure its path in
`LocalStorage.AllowedDirs`. If local storage is not needed, disable the backend:

```yaml
LocalStorage:
  Disabled: true
```

## Configuring the Funnel Server

For EC2-backed Batch jobs, Funnel records `instance_type` in the task log
metadata when the worker can reach the EC2 Instance Metadata Service using
IMDSv2. The field may be absent if metadata access is disabled or the container
cannot reach it. See the [AWS metadata access guidance](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/instancedata-data-retrieval.html)
for container networking considerations. Jobs still run if metadata is
unavailable. This field helps estimate costs but is not a bill.

Below is an example configuration. Funnel loads AWS credentials from the environment or its AWS credential provider chain when explicit credentials are omitted.

```YAML
Database: "dynamodb"
Compute: "aws-batch"
EventWriters:
  - "log"

DynamoDB:
  TableBasename: "funnel"
  AWSConfig:
    Region: "us-west-2"

AWSBatch:
  JobDefinition: "funnel-job-def"
  JobQueue: "funnel-job-queue"
  AWSConfig:
    Region: "us-west-2"

AmazonS3:
  AWSConfig:
    Region: "us-west-2"
```

### Start the server

```sh
funnel server run --config /path/to/config.yaml
```

## GPU jobs

`Compute: aws-batch` selects Funnel's AWS Batch execution code. It is a backend identifier, not an EC2 instance type or GPU model. The Batch worker must also receive `--Compute aws-batch`; newly generated Funnel job definitions include this flag. Update existing job definitions before running GPU tasks.

### Select the hardware

Use the provider documentation to choose hardware and configure the queue before submitting a TES task:

- [EC2 accelerated computing offerings](https://aws.amazon.com/ec2/instance-types/accelerated-computing/) lists instance families and their accelerator specifications.
- [AWS Batch GPU jobs](https://docs.aws.amazon.com/batch/latest/userguide/gpu-jobs.html) describes supported GPU instances and GPU reservations. EC2 accelerator offerings include hardware that Batch's GPU scheduling does not support, so check both pages.
- [GPU workload AMIs](https://docs.aws.amazon.com/batch/latest/userguide/batch-gpu-ami.html) describes the required worker-host image and driver setup.
- [Batch compute environment configuration](https://docs.aws.amazon.com/batch/latest/userguide/create-compute-environment-managed-ec2.html) describes allowed instance types and capacity settings.

To require a particular GPU model, use a queue whose associated compute environments permit only matching hardware. Naming a queue `a100-queue` does not enforce that constraint. The administrator must configure its compute environments accordingly and check availability and quotas in the selected AWS Region.

### Request GPUs through TES

For example, add these resource settings to a TES task:

```json
{
  "resources": {
    "backend_parameters_strict": true,
    "backend_parameters": {
      "gpu_count": "2",
      "job_queue": "a100-queue",
      "job_definition": "funnel-gpu-worker:2"
    }
  }
}
```

The queue and job definition in this example are deployment-defined names, not built-in Funnel presets. The server's AWS identity needs permission to submit the selected job.

| Funnel setting | AWS behavior |
| --- | --- |
| `gpu_count` | Sets the `GPU` entry in the submitted job's `containerOverrides.resourceRequirements`. |
| `job_queue` | Selects the Batch queue; defaults to `AWSBatch.JobQueue`. |
| `job_definition` | Selects the worker job definition; defaults to `AWSBatch.JobDefinition`. |
| `AWSBatch.AWSConfig.Region` | Selects the AWS Region for Batch API requests. |

### Pass allocated GPUs to the task container

AWS Batch reserves devices for the Funnel worker container. That worker launches the task container through the host Docker socket. Funnel reads the worker's allocated device IDs from `NVIDIA_VISIBLE_DEVICES`, validates them, and passes those exact IDs to Docker's `--gpus` option. The TES user supplies the request, not the allocated device IDs. AWS assigns the IDs at runtime.

A positive `gpu_count` with a missing, malformed, or mismatched allocation fails execution. Funnel does not substitute arbitrary host GPUs. Custom `Worker.Container.RunCommand` templates must include `{{.GPUArgs}}` to forward the allocation.

See [GPU tasks]({{< ref "gpus.md" >}}) for a complete submission example, request validation, and deployment verification. Physical GPU access must be verified on the deployment; request-mapping tests do not exercise AWS GPU hardware.

### Known issues

The `Task.Resources.DiskGb` field does not have any effect. See [issue 317](https://github.com/ohsu-comp-bio/funnel/issues/317).

[0]: http://docs.aws.amazon.com/amazondynamodb/latest/developerguide/Introduction.html
[1]: http://docs.aws.amazon.com/batch/latest/userguide/what-is-batch.html
[2]: http://docs.aws.amazon.com/batch/latest/userguide/Batch_GetStarted.html
[3]: https://us-west-2.console.aws.amazon.com/batch/home?region=us-west-2#/compute-environments/new
[4]: https://us-west-2.console.aws.amazon.com/batch/home?region=us-west-2#/queues/new
[5]: https://console.aws.amazon.com/iam/home?region=us-west-2#/roles$new?step=permissions&selectedService=EC2ContainerService&selectedUseCase=EC2ContainerTaskRole
[6]: https://us-west-2.console.aws.amazon.com/batch/home?region=us-west-2#/job-definitions/new
[7]: http://docs.aws.amazon.com/batch/latest/userguide/create-batch-ami.html
[8]: http://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs-optimized_AMI.html
