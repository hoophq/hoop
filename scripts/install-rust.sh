#!/bin/bash

set -e

echo "Checking if Rust is installed..."

# Check if cargo is already installed
if command -v cargo >/dev/null 2>&1; then
    echo "> Rust is already installed."
    CARGO_VERSION=$(cargo --version)
    echo "   Version: $CARGO_VERSION"
else
    echo "> Rust not found. Installing Rust..."
    echo "   This will install Rust using rustup..."
    
    # Install Rust
    curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
    
    # Source cargo environment
    source ~/.cargo/env
    
    echo "> Rust installed successfully!"
    CARGO_VERSION=$(cargo --version)
    echo "   Version: $CARGO_VERSION"
fi

echo ""
echo "Checking if cross is installed..."

# Check if cross is already installed
if command -v cross >/dev/null 2>&1; then
    echo "> cross is already installed."
    CROSS_VERSION=$(cross --version)
    echo "   Version: $CROSS_VERSION"
else
    echo "> cross not found. Installing cross..."
    echo "   This will install cross for cross-compilation..."
    
    # Install cross
    cargo install cross --git https://github.com/cross-rs/cross
    
    echo "> cross installed successfully!"
    CROSS_VERSION=$(cross --version)
    echo "   Version: $CROSS_VERSION"
fi

echo ""
echo ">  Rust setup complete!"
echo "   You can now run 'make build' or 'make build-rust'"
echo ""
echo "Available commands:"
echo "   make build-rust     - Build the Rust agent"
echo "   make build          - Build both Go and Rust binaries"
echo "   make setup-rust     - Setup Rust targets for cross-compilation"
