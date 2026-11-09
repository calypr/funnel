package run

var usage = `Usage:
  funnel run 'CMD' [flags]

General flags:
  -S, --server      Server address (or FUNNEL_SERVER; default http://localhost:8000).
  -c, --container   Containter the command runs in. Default: alpine
      --sh          Command to be run. This command will be run with the shell: 'sh -c "<sh>"'.
                    This is the default execution mode for commands passed as args. 
      --exec        Command to be run. This command will not be evaulated by 'sh'.
  -p, --print       Print the task without running it.
      --scatter     Scatter multiple tasks, one per row of the given file.
      --wait        Wait for the task to finish before exiting.
      --wait-for    Wait for the given task IDs before running the task.

Input/output file flags:
  -i, --in          Input file e.g. varname=/path/to/input.txt
  -I, --in-dir      Input directory e.g. varname=/path/to/dir
  -o, --out         Output file e.g. varname=/path/to/output.txt
  -O, --out-dir     Output directory e.g. varname=/path/to/dir
  -C, --content     Include input file content from a file e.g. varname=/path/to/in.txt
      --stdin       File to write to stdin to the command.
      --stdout      File to write to stdout of the command.
      --stderr      File to write to stderr of the command.

Resource request flags:
      --cpu         Number of CPUs to request.
      --ram         Amount of RAM to request, in GB.
      --disk        Amount of disk space to request, in GB.
      --zone        Require task be scheduled in certain zones.
      --preemptible Allow task to be scheduled on preemptible workers.

Other flags:
  -n, --name         Task name.
      --description  Task description.
      --tag          Arbitrary key-value tags, e.g. tagname=tagvalue
  -e, --env          Environment variables, e.g. envvar=foo
  -w, --workdir      Containter working directory.
      --vol          Define a volume on the container.

Examples:
  # Simple md5sum of a file and save the stdout.
  funnel run 'md5sum $in' -i in=input.txt --stdout output.txt

  # Use a different container.
  funnel run 'echo hello world' -c ubuntu

  # Print the task JSON instead of running it.
  funnel run 'echo $in" -i in=input.txt --print

  # md5sum all files in a directory.
  funnel run 'md5sum $d' -I d=./inputs --stdout output.txt

  # Sleep for 5 seconds, and wait for the sleep to finish.
  funnel run 'sleep 5' --wait

  # Set environment variables
  funnel run 'echo $MSG' -e MSG=Hello

  # When writing lots of arguments, Bash heredoc can be helpful.
  funnel run 'myprog -a $argA -b $argB -i $file1 -d $dir1' <<ARGS
    --container myorg/mycontainer
    --name 'MyProg test'
    --description 'Funnel run example of writing many arguments.'
    --tag meta=val
    --in file1=input.txt
    --in-dir dir1=inputs
    --stdout /path/to/stdout.txt
    --stderr /path/to/stderr.txt
    --env argA=1
    --env argB=2
    --vol /tmp 
    --vol /opt
    --cpu 8
    --ram 32
    --disk 100
    --preemptible
    --zone us-west1-a
  ARGS
`
