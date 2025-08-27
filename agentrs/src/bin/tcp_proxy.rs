// Copyright (c) 2025, hoop.dev
// Author: Matheus Marsiglio.
// A simple TCP proxy that forwards traffic from a local address to a target address.
// It was created to test TCP packets for the RDP protocol, but can be adapted for
// other protocols as well.
use std::io;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};

const ADDR: &str = "0.0.0.0:3389";
const TARGET_ADDR: &str = "192.168.0.57:3389";

#[tokio::main]
async fn main() -> io::Result<()> {
    let gateway_tcp = TcpListener::bind(ADDR).await?;
    println!("listening on {ADDR} → forwarding to {TARGET_ADDR}");

    loop {
        let (inbound, peer) = gateway_tcp.accept().await?;
        println!("> connection from {peer}");
        tokio::spawn(async move {
            if let Err(e) = handle_tcp(inbound).await {
                eprintln!("! proxy error: {e}");
            }
        });
    }
}

async fn handle_tcp(mut tcp: TcpStream) -> io::Result<()> {
    println!("> handling connection...");
    tcp.set_nodelay(true).ok();

    let mut outbound = TcpStream::connect(TARGET_ADDR).await?;
    outbound.set_nodelay(true).ok();

    // Split both sockets so we can treat directions separately.
    let (mut in_r, mut in_w) = tcp.split();
    let (mut out_r, mut out_w) = outbound.split();

    // Client → Target
    let c2s = async {
        let mut buf = vec![0u8; 16 * 1024];
        loop {
            let n = in_r.read(&mut buf).await?;
            println!("Read {n} bytes from client");
            if n == 0 {
                // client closed
                out_w.shutdown().await.ok();
                break;
            }

            let chunk = &buf[..n];

            out_w.write_all(chunk).await?;
        }
        Ok::<(), io::Error>(())
    };

    // Target → Client
    let s2c = async {
        let mut buf = vec![0u8; 16 * 1024];
        loop {
            let n = out_r.read(&mut buf).await?;
            println!("Read {n} bytes from target");
            if n == 0 {
                // server closed
                in_w.shutdown().await.ok();
                break;
            }

            // ---- hook: inspect/modify server->client bytes ----
            let chunk = &buf[..n];
            // Example: pass-through (no-op). Replace with your logic.
            // -------------------------------------------------------

            in_w.write_all(chunk).await?;
        }
        Ok::<(), io::Error>(())
    };

    // Run both directions until either finishes.
    tokio::select! {
        r = c2s => r?,
        r = s2c => r?,
    }

    Ok(())
}
