#!/bin/bash

# Variables
VERSION="v0.0.4"
PLATFORM=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

# Define architecture mappings if necessary
if [[ "$ARCH" == "x86_64" ]]; then
  ARCH="amd64"
fi

# Default installation directory
INSTALL_DIR="$GOPATH/bin"

# Download URL
BINARY_URL="https://github.com/alis-exchange/protoc-gen-go-jsonschema/releases/download/$VERSION/protoc-gen-go-jsonschema-$PLATFORM-$ARCH"

# Download the binary
echo "Downloading protoc-gen-go-jsonschema $VERSION for $PLATFORM/$ARCH..."
curl -L -o protoc-gen-go-jsonschema "$BINARY_URL"

# Make the binary executable
chmod +x protoc-gen-go-jsonschema

# Install the binary to the specified directory or default
echo "Installing protoc-gen-go-jsonschema to $INSTALL_DIR..."
sudo mv protoc-gen-go-jsonschema "$INSTALL_DIR/"

# Verify installation
echo "Installation complete. Verifying..."
protoc-gen-go-jsonschema --version