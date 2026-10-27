# AgentCore Runtime runs linux/arm64 images. `setup.sh runtime create`
# builds this one and pushes it to the stack's AgentRepositoryUri.
FROM public.ecr.aws/docker/library/python:3.13-slim-bookworm@sha256:e32be0e55814a08977501ca863271d9bd02443710db8f2afaf8372e2c4508530
RUN pip install --no-cache-dir boto3==1.43.119
COPY agent.py /app/agent.py
USER 10001
EXPOSE 8080
CMD ["python", "/app/agent.py"]
