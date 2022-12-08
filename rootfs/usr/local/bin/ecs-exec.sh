#!/bin/bash -e

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

       --shell (shellpath)
          The shell to use to execute commands in the container. Defaults to /bin/sh

       --interactive
          Use this flag to run your command in interactive mode.

       --base64
          Base64 the input command and decode it before executing inside the container.
          It assumes that the container has 'base64' command line in the PATH.

       --help
          Show this help
EOF
}

# read arguments
PARSED_ARGUMENTS=$(getopt \
  --options "" \
  --long cluster:,service-name:,task:,shell:,interactive,base64,help \
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
TASK_ID=
SHELL_EXEC=/bin/bash
BASE64_INPUT=0
SHELL_INTERACTIVE=0

while :
do
  case "$1" in
    --cluster)      CLUSTER_NAME="$2"; shift 2;;
    --service-name) SERVICE_NAME="$2"; shift 2;;
    --task)         TASK_ID="$2"; shift 2;;
    --shell)        SHELL_EXEC="$2"; shift 2;;
    --interactive)  SHELL_INTERACTIVE=1; shift;;
    --base64)       BASE64_INPUT=1; shift;;
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


if [ "$SHELL_INTERACTIVE" == "1" ]; then
  aws ecs execute-command \
    --cluster $CLUSTER_NAME \
    --task $TASK_ID \
    --interactive \
    --command $SHELL_EXEC
  exit $?
fi

STDIN_INPUT=$(cat -)
if [ "$BASE64_INPUT" == "1" ]; then
  STDIN_INPUT="$(base64 <<< $STDIN_INPUT)"
  unbuffer aws ecs execute-command \
    --cluster $CLUSTER_NAME \
    --task $TASK_ID \
    --interactive \
    --command "/bin/sh -c 'echo '$STDIN_INPUT' | base64 -d | '$SHELL_EXEC''"
  exit $?
fi

unbuffer aws ecs execute-command \
  --cluster $CLUSTER_NAME \
  --task $TASK_ID \
  --interactive \
  --command "$STDIN_INPUT | $SHELL_EXEC"
