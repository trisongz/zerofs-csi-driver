FROM rust:1.85-bookworm AS builder
WORKDIR /src

# Build the ZeroFS benchmark tool (expected build context: ZeroFS/bench)
COPY . .
RUN cargo build --release

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=builder /src/target/release/bench /bench
ENTRYPOINT ["/bench"]

