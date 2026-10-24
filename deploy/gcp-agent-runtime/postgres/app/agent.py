"""Support agent whose SQL tool connects through a hoop sidecar.

The sidecar runs in your VPC (deploy/sidecar/), behind an internal load
balancer. On Agent Runtime the agent reaches it over PSC-I, and the database
itself has no route from the agent. APPDB_HOST is the sidecar's address. The
sidecar answers a denied statement with a Postgres error, so the tool sees a
normal database error.
"""

import os

import psycopg
from google.adk.agents import Agent
from google.adk.apps import App
from google.adk.models import Gemini
from google.adk.tools.tool_context import ToolContext
from google.genai import types

MODEL = os.environ.get("AGENT_MODEL", "gemini-3.8-flash")


def run_sql(sql: str, tool_context: ToolContext) -> dict:
    """Runs one SQL statement on the customers database (id, name, email, plan).

    Args:
        sql: One PostgreSQL statement.

    Returns:
        The rows, the affected row count, or the database error.
    """
    # psycopg 3 on purpose. A hoop denial is FATAL 42501 and closes the
    # connection; psycopg 3 reports the message and the SQLSTATE, while
    # psycopg2 and pg8000 report only that the server closed the connection.
    try:
        with psycopg.connect(
            host=os.environ["APPDB_HOST"],  # the sidecar, never the database
            port=int(os.environ.get("APPDB_PORT", "15432")),
            dbname=os.environ.get("APPDB_NAME", "appdb"),
            user=os.environ.get("APPDB_USER", "agent"),
            password=os.environ.get("APPDB_PASSWORD", ""),
            sslmode="disable",  # the sidecar lane terminates no client TLS
            connect_timeout=10,
            # Recorded by the sidecar as postgres.option.adk.session.id.
            options=f"-c adk.session.id={tool_context.session.id}",
        ) as conn:
            cur = conn.execute(sql)
            if cur.description is None:
                return {"status": "ok", "rowcount": cur.rowcount}
            cols = [c.name for c in cur.description]
            return {"status": "ok", "rows": [dict(zip(cols, r)) for r in cur.fetchall()]}
    except psycopg.Error as e:
        return {"status": "error", "sqlstate": e.sqlstate, "error": str(e).strip()}


root_agent = Agent(
    # agents-cli copies this file as is, so the name is fixed: create the
    # project with --root-agent-name support_agent.
    name="support_agent",
    model=Gemini(model=MODEL, retry_options=types.HttpRetryOptions(attempts=3)),
    instruction=(
        "You help the support team with the customers table (id, name, email,"
        " plan). Use run_sql for every question. If run_sql returns an error,"
        " quote the exact error to the user and do not retry."
    ),
    tools=[run_sql],
)

app = App(root_agent=root_agent, name="app")
