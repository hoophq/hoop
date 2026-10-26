# ENG-880 c5, c7: the execution role from the agent's shell. Needs boto3
# (the custom image). $2: the bucket the scoped S3 endpoint must refuse.
# $3: a token vault secret ARN. $4: the harness's workload identity name.
python3 - "${2:-}" "${3:-}" "${4:-}" <<'PY'
import json, sys, time, urllib.request
import boto3, botocore
from botocore.config import Config

cfg = Config(connect_timeout=5, read_timeout=10, retries={"max_attempts": 1})
s = boto3.Session(region_name="us-east-1")
c = s.get_credentials()
print("credentials:", None if c is None else f"method={c.method} key={c.access_key[:4]}... token={'yes' if c.token else 'no'}")

def tryit(label, fn):
    t = time.time()
    try:
        r = fn()
        print(f"{label:44} OK   {str(r)[:120]}")
    except botocore.exceptions.ClientError as e:
        print(f"{label:44} DENY {e.response['Error']['Code']}: {e.response['Error']['Message'][:110]}")
    except Exception as e:
        print(f"{label:44} FAIL {type(e).__name__} after {time.time()-t:.1f}s: {str(e)[:90]}")

ac = lambda svc: s.client(svc, config=cfg)
tryit("sts GetCallerIdentity (regional)", lambda: ac("sts").get_caller_identity()["Arn"])
tryit("bedrock-runtime Converse", lambda: ac("bedrock-runtime").converse(
    modelId="us.anthropic.claude-haiku-5-5", messages=[{"role": "user", "content": [{"text": "say ok"}]}],
    inferenceConfig={"maxTokens": 5})["output"]["message"]["content"][0]["text"])
if sys.argv[1]:
    tryit(f"s3 PutObject {sys.argv[1]}", lambda: ac("s3").put_object(
        Bucket=sys.argv[1], Key="c5-exfil.txt", Body=b"data out of the VPC")["ResponseMetadata"]["HTTPStatusCode"])
tryit("ecr GetAuthorizationToken", lambda: ac("ecr").get_authorization_token()["authorizationData"][0]["expiresAt"])
tryit("ecr DescribeRepositories", lambda: [r["repositoryName"] for r in ac("ecr").describe_repositories()["repositories"]])
tryit("logs DescribeLogGroups", lambda: len(ac("logs").describe_log_groups(limit=5)["logGroups"]))
if len(sys.argv) > 2 and sys.argv[2]:
    # The vault keeps an API key here; print its length, never the value.
    tryit("secretsmanager GetSecretValue (vault key)", lambda: "len=%d" % len(
        ac("secretsmanager").get_secret_value(SecretId=sys.argv[2])["SecretString"]))
def vault_key():
    # What the harness does to fill an MCP header, done by the agent's shell.
    dp = ac("bedrock-agentcore")
    wat = dp.get_workload_access_token(workloadName=sys.argv[3])["workloadAccessToken"]
    key = dp.get_resource_api_key(workloadIdentityToken=wat, resourceCredentialProviderName="eng880-api")["apiKey"]
    return "len=%d" % len(key)
if len(sys.argv) > 3 and sys.argv[3]:
    tryit("bedrock-agentcore GetResourceApiKey eng880-api", vault_key)
tryit("bedrock-agentcore-control ListHarnesses", lambda: len(ac("bedrock-agentcore-control").list_harnesses()["harnesses"]))
PY
