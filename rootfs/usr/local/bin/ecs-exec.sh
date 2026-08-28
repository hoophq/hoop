#!/bin/bash
set -eo pipefail

[[ "$CONNECTION_DEBUG" == "1" ]] && set -x

Help()
{
   cat <<EOF
DESCRIPTION
       Runs a command remotely on a container within a task on ECS.

OPTIONS
       --cluster (cluster-name)
          The Amazon Resource Name (ARN) or short name of the cluster the task
          is running in. If you do not specify a cluster, the default cluster
          is assumed.

       --container (string)
          The name of the container to execute the command on. A container
          name only needs to be specified for tasks containing multiple
          containers.

       --service-name (service-name)
          The name of the service to use when filtering the ListTasks results.
          Specifying a serviceName limits the results to tasks that belong to
          that service.

       --task (task-id)
          The Amazon Resource Name (ARN) or ID of the task the container is
          part of. Defaults to the first task ID found by its service

       --pipe (command)
          The command which will be used against the input. This mode requires that
          a base64 command is available in the image.

       --interactive
          Use this flag to run your command in interactive mode.

       --help
          Show this help
EOF
}

# read arguments
PARSED_ARGUMENTS=$(getopt \
  --options "" \
  --long cluster:,service-name:,container:,task:,pipe:,interactive,help \
  --name "$(basename "$0")" \
  -- "$@"
)
VALID_ARGUMENTS=$?
if [ "$VALID_ARGUMENTS" != "0" ]; then
  usage
fi

eval set -- "$PARSED_ARGUMENTS"

CLUSTER_NAME=
SERVICE_NAME=
CONTAINER=
TASK_ID=
PIPE_EXEC=
SHELL_INTERACTIVE=0

while :
do
  case "$1" in
    --cluster)      CLUSTER_NAME="$2"; shift 2;;
    --service-name) SERVICE_NAME="$2"; shift 2;;
    --container)    CONTAINER="$2"; shift 2;;
    --task)         TASK_ID="$2"; shift 2;;
    --pipe)         PIPE_EXEC="$2"; shift 2;;
    --interactive)  SHELL_INTERACTIVE=1; shift;;
    --help)         Help; exit 0 ;;
    # -- means the end of the arguments; drop this, and break out of the while loop
    --) shift; break ;;
    # If invalid options were passed, then getopt should have reported an error,
    # which we checked as VALID_ARGUMENTS when getopt was called...
    *) echo "Unexpected option: $1"; break;;
  esac
done

: "${CLUSTER_NAME:? Required argument --cluster not set}"

if [ -z $TASK_ID ]; then
  : "${SERVICE_NAME:? Required argument --service-name not set}"
  TASK_ID=$(aws ecs list-tasks \
  	  --cluster $CLUSTER_NAME \
	    --service-name $SERVICE_NAME \
	    --max-items 1| jq .taskArns[0] -r)
fi

if [ -z $CONTAINER ]; then
  CONTAINER=$(aws ecs describe-tasks \
    --cluster $CLUSTER_NAME \
    --tasks $TASK_ID | jq .tasks[].containers[0].name -r)
fi

if [ "$SHELL_INTERACTIVE" == "1" ]; then
  : "${PIPE_EXEC:? Required argument --pipe not set}"
  aws ecs execute-command \
    --cluster $CLUSTER_NAME \
    --task $TASK_ID \
    --container $CONTAINER \
    --interactive \
    --command "$PIPE_EXEC"
  exit $?
fi

# aws ecs execute-command has no stdin channel to the remote container, so
# the whole payload must fit inside the --command argument. The Linux kernel
# caps any single execve() argument at MAX_ARG_STRLEN (128 KiB on 4Ki-page
# systems), independent of the much larger total ARG_MAX -- confirmed via
# binary search on the same x86_64/Ubuntu-noble image this script ships in
# (Dockerfile.tools): a 131071-byte argument execs fine, 131072 fails with
# "Argument list too long" (exit 126). execve() counts the argument's NUL
# terminator against MAX_ARG_STRLEN, so usable content tops out one byte
# short of it.
#
# --cli-input-json file://<path> was tried as a way past this: it does avoid
# the *local* exec limit (the AWS CLI reads the request body from disk, not
# argv), but it doesn't help -- confirmed empirically against a real running
# task. AWS's ECS Exec / SSM agent runs the command *inside the container*
# via the same fork/exec("/bin/sh", ["-c", command]) mechanism, which hits
# the identical kernel argument limit on the remote side (~131KB, "Failed to
# start pty: fork/exec /bin/sh: argument list too long"). The bottleneck
# isn't how the request reaches AWS; it's how ECS Exec runs it once there.
# There is no way to exceed this via aws ecs execute-command.

# unbuffer is required when running one-off tasks
# https://github.com/aws/amazon-ssm-agent/issues/354#issuecomment-817274498
STDIN_INPUT=$(cat -)
if [ -n "$PIPE_EXEC" ]; then
  STDIN_INPUT="$(base64 -w0 <<< $STDIN_INPUT)"
  unbuffer aws ecs execute-command \
    --cluster $CLUSTER_NAME \
    --task $TASK_ID \
    --container $CONTAINER \
    --interactive \
    --command "/bin/sh -e -c 'echo -n $STDIN_INPUT | base64 -d | $PIPE_EXEC'"
  exit $?
fi

unbuffer aws ecs execute-command \
  --cluster $CLUSTER_NAME \
  --task $TASK_ID \
  --container $CONTAINER \
  --interactive \
  --command "$STDIN_INPUT"
