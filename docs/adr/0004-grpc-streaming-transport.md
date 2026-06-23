# ADR-0004: gRPC streaming as the MQ transport

- Status: Accepted
- Date: 2026-06-24

## Context
The custom broker needs a wire protocol between clients (streamers/collectors) and the broker
service. Options: hand-rolled length-prefixed TCP, gRPC streaming, or HTTP/2 REST. We still build all
*queue semantics* ourselves — this decision is only about the transport.

## Decision
Use **gRPC with streaming RPCs**:
- `Produce(stream ProduceRequest) returns (stream ProduceResponse)` — pipelined, high-throughput
  publish with per-message offset acks.
- `Consume(ConsumeRequest) returns (stream ConsumeResponse)` — server-push delivery to a consumer
  group member.
- `Commit`, `CreateTopic`, `Health` — unary admin/ack RPCs.
Contract defined in `mq/proto/mq.proto`, codegen into `mq/gen/mqv1`.

## Driving Prompt
Confirmed via question prompt: **MQ transport = "gRPC streaming"**.

## Consequences
- (+) HTTP/2 multiplexing, backpressure, and typed client lib for free; idiomatic Go.
- (+) Streaming maps naturally onto produce/consume; cheap to add fields via protobuf.
- (−) Adds protobuf codegen to the build (handled hermetically — see ADR-0006).
- (−) Must be explicit that gRPC ≠ a broker; queue durability/ordering/offsets are ours (ADR-0001).

## Alternatives considered
- **Custom TCP framing** — max "built it ourselves" credibility but more error-prone hand-written
  encode/decode and connection management for no real gain over gRPC here.
- **HTTP/2 REST long-poll** — easiest to curl-debug, but awkward streaming consume and higher
  per-message overhead.
