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

Below is an example configuration. Note that the `Key`
and `Secret` fields are left blank in the configuration of the components. This is because
Funnel will, by default, try to automatically load credentials from the environment.
Alternatively, you may explicitly set the credentials in the config.

```YAML
Database: "dynamodb"
Compute: "aws-batch"
EventWriters:
  - "log"

Dynamodb:
  TableBasename: "funnel"
  Region: "us-west-2"
  Key: ""
  Secret: ""

Batch:
  JobDefinition: "funnel-job-def"
  JobQueue: "funnel-job-queue"
  Region: "us-west-2"
  Key: ""
  Secret: ""

AmazonS3:
  Key: ""
  Secret: ""
```

### Start the server

```sh
funnel server run --config /path/to/config.yaml
```

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
