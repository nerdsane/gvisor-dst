# Building gVisor with DST Support

This document explains how to build the modified gVisor runtime with
Deterministic Simulation Testing (DST) support.

## Prerequisites

### 1. Install Bazel

gVisor requires Bazel for building. Install Bazelisk (recommended):

```bash
# Linux (x86_64)
curl -L https://github.com/bazelbuild/bazelisk/releases/latest/download/bazelisk-linux-amd64 \
  -o /usr/local/bin/bazel
chmod +x /usr/local/bin/bazel

# Or via apt
sudo apt install bazel

# Verify installation
bazel --version
```

### 2. Install Build Dependencies

```bash
# Ubuntu/Debian
sudo apt-get install -y \
    build-essential \
    git \
    python3 \
    pkg-config \
    libseccomp-dev
```

## Building the DST-Enabled Runtime

### Quick Build

```bash
cd gvisor

# Build runsc with DST support
bazel build //runsc:runsc --config=x86_64

# The binary will be at:
# bazel-bin/runsc/runsc_/runsc
```

### Development Build (with debug symbols)

```bash
bazel build //runsc:runsc --config=x86_64 --compilation_mode=dbg
```

### Running Tests

```bash
# Run DST package tests
bazel test //pkg/sentry/dst:dst_test

# Run all affected tests
bazel test //pkg/sentry/...
```

## Installing the Built Runtime

```bash
# Copy to system location
sudo cp bazel-bin/runsc/runsc_/runsc /usr/local/bin/runsc-dst

# Or install alongside regular gVisor
sudo runsc-dst install --runtime=runsc-dst

# Configure Docker
sudo tee /etc/docker/daemon.json <<EOF
{
    "runtimes": {
        "runsc": {
            "path": "/usr/local/bin/runsc"
        },
        "runsc-dst": {
            "path": "/usr/local/bin/runsc-dst"
        }
    }
}
EOF

sudo systemctl restart docker
```

## DST-Specific Build Flags

The DST functionality is controlled by build tags:

```bash
# Build with DST enabled (default for this fork)
bazel build //runsc:runsc --define dst=enabled

# Build without DST (vanilla gVisor)
bazel build //runsc:runsc --define dst=disabled
```

## Build Targets

| Target | Description |
|--------|-------------|
| `//runsc:runsc` | Main runtime binary with DST |
| `//pkg/sentry/dst:dst` | DST core library |
| `//pkg/sentry/dst:dst_test` | DST unit tests |
| `//pkg/sentry/fsimpl/dst:dst` | Deterministic filesystem |
| `//pkg/tcpip/link/dst:dst` | Deterministic network |

## Directory Structure

```
gvisor/
├── pkg/sentry/dst/           # Core DST infrastructure
│   ├── bloodhound.go         # Fault injection, property checking
│   ├── snapshot.go           # CoW snapshots, state trees
│   └── BUILD                  # Bazel build file
├── pkg/sentry/fsimpl/dst/    # Deterministic filesystem
│   ├── dst.go
│   └── BUILD
├── pkg/tcpip/link/dst/       # Deterministic network
│   ├── dst.go
│   └── BUILD
├── patches/                   # Integration patches
│   ├── 005-dst-filesystem.patch
│   ├── 006-dst-snapshot.patch
│   └── 007-bloodhound-integration.patch
├── DST_README.md             # Full DST documentation
└── BUILD_DST.md              # This file
```

## Verifying the Build

After building, verify DST support:

```bash
# Check version
./bazel-bin/runsc/runsc_/runsc --version

# Check DST flags are available
./bazel-bin/runsc/runsc_/runsc --help | grep dst

# Should show:
#   --dst              Enable DST mode
#   --dst-seed         Random seed for deterministic execution
#   --dst-max-steps    Maximum simulation steps
#   --dst-control-socket  Unix socket for DST control
```

## Integration with Bloodhound

The Bloodhound Rust crate communicates with gVisor DST via:

1. **Control Socket**: Unix socket at `--dst-control-socket` path
2. **JSON Protocol**: Commands/responses in newline-delimited JSON

To use with Bloodhound:

```bash
# Start container with DST
runsc-dst --dst --dst-seed=42 --dst-control-socket=/run/gvisor/control.sock \
    run container-id

# Bloodhound connects to the socket and sends commands like:
# {"type":"Step","data":{"steps":100}}
# {"type":"Snapshot","data":{"id":"snap-1"}}
```

## Troubleshooting

### Build Errors

**Missing go_library**: Ensure you have the full gVisor source, not just the DST files:
```bash
git submodule update --init
```

**Bazel version mismatch**: Use Bazelisk to auto-select the correct version:
```bash
# Bazelisk reads .bazelversion file automatically
bazelisk build //runsc:runsc
```

**Missing seccomp**: Install libseccomp development headers:
```bash
sudo apt-get install libseccomp-dev
```

### Runtime Errors

**Socket permission denied**: Ensure the socket directory exists and is writable:
```bash
sudo mkdir -p /run/gvisor
sudo chmod 777 /run/gvisor
```

**DST not enabled**: Check the binary was built with DST:
```bash
runsc-dst --help | grep -c dst  # Should be > 0
```

## Development Workflow

1. Make changes to DST code in `pkg/sentry/dst/`
2. Run tests: `bazel test //pkg/sentry/dst:dst_test`
3. Build runtime: `bazel build //runsc:runsc`
4. Test with container: `./bazel-bin/runsc/runsc_/runsc --dst run test`

## Continuous Integration

The gVisor fork is tested in CI with:

```yaml
# .github/workflows/gvisor-dst.yml
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Build gVisor DST
        run: |
          cd gvisor
          bazel build //runsc:runsc --config=x86_64
      - name: Test DST
        run: |
          cd gvisor
          bazel test //pkg/sentry/dst:dst_test
```

## Known Limitations

1. **x86_64 only**: DST mode is currently only tested on x86_64
2. **Single vCPU**: Determinism requires single-threaded execution
3. **No KVM**: DST uses ptrace platform, not KVM

## Related Documentation

- [DST_README.md](./DST_README.md) - Full DST architecture and usage
- [patches/](./patches/) - Integration patches with explanations
- [../docs/adr/](../docs/adr/) - Architecture decision records
