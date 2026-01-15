#!/bin/bash

set -e  # Exit immediately on error

# Usage information
usage() {
  echo "Usage: ./install.sh [OPTIONS]"
  echo ""
  echo "Options:"
  echo "  -v, --version <version>  Specify version to install (e.g., v0.0.5)"
  echo "  -h, --help               Show this help message"
  echo ""
  echo "If no version is specified, the latest release will be installed."
  echo ""
  echo "Examples:"
  echo "  ./install.sh                    # Install latest version"
  echo "  ./install.sh -v v0.0.5          # Install specific version"
  echo "  ./install.sh --version v0.0.5   # Install specific version"
}

# Parse arguments
VERSION=""
while [[ $# -gt 0 ]]; do
  case $1 in
    -v|--version)
      VERSION="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Error: Unknown option $1"
      usage
      exit 1
      ;;
  esac
done

# If no version specified, fetch the latest release
if [ -z "$VERSION" ]; then
  echo "Fetching latest release version..."
  VERSION=$(curl -s https://api.github.com/repos/alis-exchange/protoc-gen-go-jsonschema/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
  if [ -z "$VERSION" ]; then
    echo "Error: Could not determine latest version. Please specify a version with -v flag."
    exit 1
  fi
fi

# Detect platform and architecture
PLATFORM=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

# Define architecture mappings
if [[ "$ARCH" == "x86_64" ]]; then
  ARCH="amd64"
elif [[ "$ARCH" == "aarch64" ]]; then
  ARCH="arm64"
fi

# Default installation directory
if [ -z "$GOPATH" ]; then
  INSTALL_DIR="/usr/local/bin"
else
  INSTALL_DIR="$GOPATH/bin"
fi

# Download URL
BINARY_URL="https://github.com/alis-exchange/protoc-gen-go-jsonschema/releases/download/$VERSION/protoc-gen-go-jsonschema-$PLATFORM-$ARCH"

# Download the binary
echo "Downloading protoc-gen-go-jsonschema $VERSION for $PLATFORM/$ARCH..."
if ! curl -fL -o protoc-gen-go-jsonschema "$BINARY_URL"; then
  echo "Error: Failed to download binary. Check that version $VERSION exists and has a binary for $PLATFORM/$ARCH."
  exit 1
fi

# Make the binary executable
chmod +x protoc-gen-go-jsonschema

# Install the binary to the specified directory
echo "Installing protoc-gen-go-jsonschema to $INSTALL_DIR..."
if [ -w "$INSTALL_DIR" ]; then
  mv protoc-gen-go-jsonschema "$INSTALL_DIR/"
else
  sudo mv protoc-gen-go-jsonschema "$INSTALL_DIR/"
fi

# Verify installation
echo "Installation complete. Verifying..."
protoc-gen-go-jsonschema --version