# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# Modified by hoop: agents-cli 1.11.0's base Dockerfile, plus UV_NO_SYNC below.
# Listed in render_files (.template/templateconfig.yaml), so agents-cli fills
# in its cookiecutter fields as it does for its own copy.

FROM python:3.12-slim

# Install uv
COPY --from=ghcr.io/astral-sh/uv:0.8.13 /uv /uvx /bin/

WORKDIR /code

COPY ./pyproject.toml ./README.md ./uv.lock* ./

COPY ./{{cookiecutter.agent_directory}} ./{{cookiecutter.agent_directory}}

RUN uv sync --frozen

ARG AGENT_VERSION=0.0.0
ENV AGENT_VERSION=${AGENT_VERSION}

{%- if cookiecutter.agent_gateway %}

# Install the Agent Gateway root CA passed by the platform.
# Based on https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/runtime/agent-gateway-runtime-deploy#configure-byoc
ARG AGENT_GATEWAY_ROOT_CERTIFICATES
RUN if [ -n "$AGENT_GATEWAY_ROOT_CERTIFICATES" ]; then \
      mkdir -p /usr/local/share/ca-certificates; \
      printf "%b" "$AGENT_GATEWAY_ROOT_CERTIFICATES" \
        | awk 'BEGIN {c=0} /BEGIN CERTIFICATE/ {c++} c > 0 { print > "/usr/local/share/ca-certificates/agw-" c ".crt" }'; \
      update-ca-certificates; \
    fi

# If Agent Gateway root CA was provided, configure SSL/TLS trust paths.
ENV SSL_CERT_FILE=${AGENT_GATEWAY_ROOT_CERTIFICATES:+/etc/ssl/certs/ca-certificates.crt}
ENV REQUESTS_CA_BUNDLE=${AGENT_GATEWAY_ROOT_CERTIFICATES:+/etc/ssl/certs/ca-certificates.crt}
ENV GRPC_DEFAULT_SSL_ROOTS_FILE_PATH=${AGENT_GATEWAY_ROOT_CERTIFICATES:+/etc/ssl/certs/ca-certificates.crt}
{%- endif %}

EXPOSE 8080

# `uv sync` above installed everything. Without this, `uv run` builds the
# project again at every container start, and once uv's index cache is stale
# that needs pypi.org: on Agent Runtime with PSC-I the fetch timed out, and the
# agent stopped starting after its first scale to zero.
ENV UV_NO_SYNC=1

CMD ["uv", "run", "uvicorn", "{{cookiecutter.agent_directory}}.fast_api_app:app", "--host", "0.0.0.0", "--port", "8080"]
