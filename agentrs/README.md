# Hoop Rust Agent

 This agent contains RDP functionality.

## Running the agent 

```bash
cargo run --bin agentrs
```

## Agent configuration

```bash
export GATEWAY_URL=ws://<gateway_url>
```

## Testing with dummy gateway

This project contains a dummy gateway for testing purposes. 
Execute the following command to run it:

This will run the dummy gateway on ws://localhost:8009 and a tcp for the rdp client
in the gw dummy you can mock your real rdp server for testing

```bashbash
cargo run --example gw

```

then use a client to access the rdp server

```bash
"xfreerdp /u:fake /p:fake /v:localhost:3389
```

this will redirect youto the rdp passing trought the agent proxy

