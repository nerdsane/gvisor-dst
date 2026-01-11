#!/bin/bash
# Build gVisor with DST Support
#
# This script builds the modified gVisor runtime with Deterministic
# Simulation Testing (DST) support enabled.
#
# Usage:
#   ./scripts/build-dst.sh          # Build release
#   ./scripts/build-dst.sh --debug  # Build with debug symbols
#   ./scripts/build-dst.sh --test   # Build and run tests
#   ./scripts/build-dst.sh --install # Build and install to system

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GVISOR_DIR="$(dirname "$SCRIPT_DIR")"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1"
    exit 1
}

# Check prerequisites
check_prereqs() {
    info "Checking prerequisites..."

    if ! command -v bazel &> /dev/null && ! command -v bazelisk &> /dev/null; then
        error "Bazel is not installed. Please install bazel or bazelisk first."
    fi

    if ! command -v go &> /dev/null; then
        warn "Go is not installed. Some development tools may not work."
    fi

    if [[ ! -f /usr/include/seccomp.h ]]; then
        warn "libseccomp-dev not found. Install with: sudo apt-get install libseccomp-dev"
    fi

    info "Prerequisites OK"
}

# Build the runtime
build_runtime() {
    local mode="${1:-release}"

    info "Building gVisor with DST support (mode: $mode)..."

    cd "$GVISOR_DIR"

    local build_args=(
        "//runsc:runsc"
        "--config=x86_64"
    )

    if [[ "$mode" == "debug" ]]; then
        build_args+=("--compilation_mode=dbg")
    fi

    # Run bazel build
    if command -v bazelisk &> /dev/null; then
        bazelisk build "${build_args[@]}"
    else
        bazel build "${build_args[@]}"
    fi

    local output_path="$GVISOR_DIR/bazel-bin/runsc/runsc_/runsc"

    if [[ -f "$output_path" ]]; then
        info "Build successful!"
        info "Binary at: $output_path"

        # Show DST flags
        if "$output_path" --help 2>&1 | grep -q dst; then
            info "DST support: ENABLED"
        else
            warn "DST support: NOT DETECTED (build may need configuration)"
        fi
    else
        error "Build failed - binary not found at $output_path"
    fi
}

# Run tests
run_tests() {
    info "Running DST tests..."

    cd "$GVISOR_DIR"

    local test_targets=(
        "//pkg/sentry/dst:dst_test"
    )

    # Check if fsimpl/dst and tcpip/link/dst have tests
    if [[ -f "$GVISOR_DIR/pkg/sentry/fsimpl/dst/BUILD" ]]; then
        if grep -q "dst_test" "$GVISOR_DIR/pkg/sentry/fsimpl/dst/BUILD"; then
            test_targets+=("//pkg/sentry/fsimpl/dst:dst_test")
        fi
    fi

    if [[ -f "$GVISOR_DIR/pkg/tcpip/link/dst/BUILD" ]]; then
        if grep -q "dst_test" "$GVISOR_DIR/pkg/tcpip/link/dst/BUILD"; then
            test_targets+=("//pkg/tcpip/link/dst:dst_test")
        fi
    fi

    info "Test targets: ${test_targets[*]}"

    if command -v bazelisk &> /dev/null; then
        bazelisk test "${test_targets[@]}" --test_output=errors
    else
        bazel test "${test_targets[@]}" --test_output=errors
    fi

    info "Tests passed!"
}

# Install to system
install_runtime() {
    local install_path="${1:-/usr/local/bin/runsc-dst}"

    info "Installing to $install_path..."

    local source_path="$GVISOR_DIR/bazel-bin/runsc/runsc_/runsc"

    if [[ ! -f "$source_path" ]]; then
        error "Binary not found. Run build first: $0"
    fi

    sudo cp "$source_path" "$install_path"
    sudo chmod +x "$install_path"

    info "Installed to $install_path"

    # Optionally configure Docker
    if command -v docker &> /dev/null; then
        info "Configuring Docker runtime..."

        # Check if already configured
        if docker info 2>/dev/null | grep -q "runsc-dst"; then
            info "Docker runtime 'runsc-dst' already configured"
        else
            info "To configure Docker, add to /etc/docker/daemon.json:"
            cat <<EOF
{
    "runtimes": {
        "runsc-dst": {
            "path": "$install_path"
        }
    }
}
EOF
            info "Then run: sudo systemctl restart docker"
        fi
    fi
}

# Show usage
usage() {
    cat <<EOF
Usage: $0 [OPTIONS]

Build gVisor with DST (Deterministic Simulation Testing) support.

Options:
    --release       Build release version (default)
    --debug         Build with debug symbols
    --test          Build and run tests
    --install [PATH] Build and install to system (default: /usr/local/bin/runsc-dst)
    --help          Show this help message

Examples:
    $0                      # Build release
    $0 --debug              # Build debug version
    $0 --test               # Build and run tests
    $0 --install            # Build and install to default location
    $0 --install /opt/runsc # Build and install to custom location

Environment:
    BAZEL_FLAGS     Additional flags to pass to bazel

EOF
}

# Main
main() {
    local do_build=true
    local do_test=false
    local do_install=false
    local build_mode="release"
    local install_path="/usr/local/bin/runsc-dst"

    while [[ $# -gt 0 ]]; do
        case $1 in
            --release)
                build_mode="release"
                shift
                ;;
            --debug)
                build_mode="debug"
                shift
                ;;
            --test)
                do_test=true
                shift
                ;;
            --install)
                do_install=true
                if [[ $# -gt 1 && ! "$2" =~ ^-- ]]; then
                    install_path="$2"
                    shift
                fi
                shift
                ;;
            --help|-h)
                usage
                exit 0
                ;;
            *)
                error "Unknown option: $1"
                ;;
        esac
    done

    check_prereqs

    if [[ "$do_build" == true ]]; then
        build_runtime "$build_mode"
    fi

    if [[ "$do_test" == true ]]; then
        run_tests
    fi

    if [[ "$do_install" == true ]]; then
        install_runtime "$install_path"
    fi

    info "Done!"
}

main "$@"
