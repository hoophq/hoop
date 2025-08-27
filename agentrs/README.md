# Hoop Rust Agent

 This agent contains RDP functionality.

## Running the agent

```bash
cargo run --bin agentrs
```

## Agent configuration

```bash
export WINDOWS_TARGET=<ip_address>

export GATEWAY_URL=ws://<gateway_url>
```

## Testing with dummy gateway

This project contains a dummy gateway for testing purposes. Execute the following command to run it:

```bashbash
cargo run --bin gateway
```

