#!/bin/bash
# Privacy-First PicoClaw Build Script
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    local color=$1
    local message=$2
    echo -e "${color}${message}${NC}"
}

# Find Go installation
find_go() {
    local go_paths=(
        "/usr/local/go/bin/go"
        "$HOME/go/bin/go"
        "/opt/homebrew/bin/go"
        "$(which go 2>/dev/null)"
    )
    
    for go_path in "${go_paths[@]}"; do
        if [[ -x "$go_path" ]]; then
            echo "$go_path"
            return 0
        fi
    done
    return 1
}

# Main build function
build_webui() {
    print_status "$YELLOW" "🔨 Building Privacy-First PicoClaw WebUI..."
    
    # Find Go
    GO_BIN=$(find_go)
    if [[ -z "$GO_BIN" ]]; then
        print_status "$RED" "❌ Error: Go not found!"
        echo ""
        echo "Please install Go 1.25+:"
        echo "  macOS: brew install go"
        echo "  Linux: curl -fsSL https://golang.org/dl/go1.25.7.linux-amd64.tar.gz | sudo tar -C /usr/local -xzf"
        echo ""
        echo "Or download from: https://golang.org/dl/"
        exit 1
    fi
    
    print_status "$GREEN" "✓ Found Go at: $GO_BIN"
    
    # Show Go version
    GO_VERSION=$("$GO_BIN" version | awk '{print $3}')
    print_status "$GREEN" "✓ Go version: $GO_VERSION"
    
    # Create bin directory
    mkdir -p bin
    
    # Build WebUI
    print_status "$YELLOW" "Building WebUI..."
    cd cmd/webui
    "$GO_BIN" build -o ../../bin/webui .
    cd ../..
    
    if [[ -f "bin/webui" ]]; then
        print_status "$GREEN" "✓ WebUI built successfully: bin/webui"
        print_status "$GREEN" "✓ Size: $(du -h bin/webui | cut -f1)"
    else
        print_status "$RED" "❌ Build failed!"
        exit 1
    fi
    
    # Build CLI (optional)
    if command -v picoclaw &> /dev/null || [[ "$1" == "--with-cli" ]]; then
        print_status "$YELLOW" "Building CLI..."
        cd cmd/picoclaw
        "$GO_BIN" build -o ../../bin/picoclaw .
        cd ../..
        
        if [[ -f "bin/picoclaw" ]]; then
            print_status "$GREEN" "✓ CLI built successfully: bin/picoclaw"
            print_status "$GREEN" "✓ Size: $(du -h bin/picoclaw | cut -f1)"
        fi
    fi
    
    echo ""
    print_status "$GREEN" "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    print_status "$GREEN" "✓ Build complete!"
    print_status "$GREEN" "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    print_status "$YELLOW" "To start the WebUI:"
    print_status "$NC" "  ./bin/webui"
    print_status "$NC" "  ./bin/webui 9090  # custom port"
    echo ""
    print_status "$YELLOW" "Then open your browser:"
    print_status "$NC" "  http://localhost:8080"
    echo ""
    print_status "$YELLOW" "For more info, see:"
    print_status "$NC" "  https://github.com/sipeed/picoclaw/blob/main/WEBUI_QUICKSTART.md"
}

# Check for flags
case "$1" in
    --help|-h)
        echo "Privacy-First PicoClaw Build Script"
        echo ""
        echo "Usage: ./build.sh [options]"
        echo ""
        echo "Options:"
        echo "  --with-cli    Also build the CLI binary"
        echo "  --help, -h    Show this help message"
        echo ""
        echo "Examples:"
        echo "  ./build.sh           # Build WebUI only"
        echo "  ./build.sh --with-cli # Build both WebUI and CLI"
        exit 0
        ;;
    *)
        build_webui "$@"
        ;;
esac
