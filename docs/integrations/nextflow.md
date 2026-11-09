# Nextflow

> ⚠️ Nextflow support is currently in development and requires a few additional steps to run which are included below.

[Nextflow](https://nextflow.io/) is a workflow engine with a rich ecosystem of pipelines centered around biological analysis.

> Nextflow enables scalable and reproducible scientific workflows using software containers. It allows the adaptation of pipelines written in the most common scripting languages.

> Its fluent DSL simplifies the implementation and the deployment of complex parallel and reactive workflows on clouds and clusters. 

Since Nextflow [includes support](https://www.nextflow.io/docs/latest/executor.html#ga4gh-tes) for the TES API, it can be used in conjunction with Funnel to run tasks or to interact with a common TES endpoint.  

## Getting Started

To set up Nextflow to use Funnel as the TES executor, run the following steps:

### 1. Install Nextflow

*Adapted from the [Nextflow Documentation](https://nextflow.io/docs/latest/install.html)*

#### a. Install Nextflow:

```sh
curl -s https://get.nextflow.io | bash
```

This will create the nextflow executable in the current directory.

#### b. Make Nextflow executable:

```sh
chmod +x nextflow
```

#### c. Move Nextflow into an executable path:

```sh
sudo mv nextflow /usr/local/bin
```

#### d. Confirm that Nextflow is installed correctly:

```sh
nextflow info
```

### 2. Update Nextflow Config

Add the following to your `nextflow.config` in order to use the GA4GH TES plugin:

```yaml
cat <<EOF >> nextflow.config
plugins {
  id 'nf-ga4gh'
}

process.executor = 'tes'
tes.endpoint = 'http://localhost:8000'   # <--- Funnel's default address 
EOF
```

### 3. Start the Funnel Server

Start the Funnel server:

```sh
funnel server run
```
 
### 4. Run Nextflow

In another window, run the workflow:

```sh
nextflow run main.nf -c nextflow.config
```

## Additional Resources

- [Nextflow Homepage](https://nextflow.io/)

- [Nextflow Documentation](https://www.nextflow.io/docs)

- [Nextflow's TES Support](https://www.nextflow.io/docs/latest/executor.html#ga4gh-tes)

- [nf-core](https://nf-co.re/)
  > A community effort to collect a curated set of analysis pipelines built using Nextflow. 

- [nf-canary](https://github.com/seqeralabs/nf-canary)
  > A minimal Nextflow workflow for testing infrastructure. 

- [Nextflow Patterns](https://nextflow-io.github.io/patterns/)
  > A curated collection of Nextflow implementation patterns 
