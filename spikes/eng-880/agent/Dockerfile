# ENG-880 agent image, linux/arm64. Throwaway.
# The harness uses it as its environment and overrides ENTRYPOINT and CMD
# (c4, c8). AgentCore Runtime runs CMD as the agent (c2).
FROM public.ecr.aws/docker/library/python:3.13-slim-bookworm
RUN apt-get update \
 && apt-get install -y --no-install-recommends postgresql-client curl jq dnsutils netcat-openbsd ca-certificates procps \
 && rm -rf /var/lib/apt/lists/* \
 && pip install --no-cache-dir boto3==1.40.*
# The sidecar's CA: the lanes serve a certificate for sidecar.eng880.internal.
COPY ca.crt /usr/local/share/ca-certificates/eng880-ca.crt
RUN update-ca-certificates
COPY agent.py /app/agent.py
EXPOSE 8080
CMD ["python", "/app/agent.py"]
