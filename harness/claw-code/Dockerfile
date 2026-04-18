# Claw Code harness for KARO
# Claw Code is a Rust reimplementation of Claude Code (MIT licensed)
# https://github.com/ultraworkers/claw-code

# --- Build stage: compile claw-cli from source ---
# Use nightly Rust for edition2024 support required by claw-code dependencies
FROM rustlang/rust:nightly-slim AS builder

RUN apt-get update && \
    apt-get install -y --no-install-recommends git pkg-config libssl-dev && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /build
RUN git clone --depth 1 https://github.com/ultraworkers/claw-code.git .

WORKDIR /build/rust
RUN cargo build --release --package claw-cli && \
    cp target/release/claw /usr/local/bin/claw

# --- Runtime stage ---
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates curl gettext-base git && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/local/bin/claw /usr/local/bin/claw

RUN mkdir -p /workspace

COPY bootstrap-claw-code.sh /usr/local/bin/karo-bootstrap
COPY build-task-prompt.sh /usr/local/bin/karo-build-task-prompt
COPY .claw.json.template /etc/karo/mcp-template.json

RUN chmod +x /usr/local/bin/karo-bootstrap \
    /usr/local/bin/karo-build-task-prompt

WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/karo-bootstrap"]
