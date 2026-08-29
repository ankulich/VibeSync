# VibeSync Architecture Overview

This document is the entry point to the system's architecture. It maps the
microservices, their data stores, and the event flow. Per-decision rationale
lives in `docs/adr/`.

## Service map (target end-state)

```mermaid
graph TB
    subgraph Clients
        WEB[Web Frontend<br/>React + Vite]
        MOB[Mobile / Native]
    end

    GW[API Gateway<br/>auth, routing, rate-limit]

    subgraph Core Services
        AUTH[Auth Service<br/>JWT, OAuth2, JWKS]
        USER[User Service<br/>profiles, roles]
        ROOM[Room Service<br/>lifecycle, membership]
        SYNC[Sync Service<br/>authoritative clock]
        PLAY[Playback Service<br/>executes sync commands]
    end

    subgraph Media Services
        MEDIA[Media Service<br/>catalog, queue]
        PROV[Provider Service<br/>Spotify, YouTube]
        STORAGE[Storage Service<br/>MinIO wrapper]
    end

    subgraph Async
        NOTIF[Notification Service]
        ANAL[Analytics Service]
    end

    subgraph Infrastructure
        PG[(Postgres)]
        REDIS[(Redis)]
        MONGO[(MongoDB)]
        KAFKA[[Kafka]]
        MINIO[(MinIO)]
        OTEL[OTel + Jaeger<br/>+ Prometheus]
    end

    WEB --> GW
    MOB --> GW
    GW --> AUTH
    GW --> USER
    GW --> ROOM
    GW --> SYNC
    GW --> MEDIA

    AUTH -. reads/writes .-> PG
    USER -. reads/writes .-> PG
    ROOM -. reads/writes .-> PG
    SYNC -. state cache .-> REDIS
    SYNC -. events .-> KAFKA
    PLAY --> SYNC
    MEDIA --> PROV
    MEDIA --> STORAGE
    STORAGE --> MINIO
    NOTIF -. consumes .-> KAFKA
    ANAL -. consumes .-> KAFKA
    ANAL -. writes .-> MONGO

    AUTH -. traces .-> OTEL
    SYNC -. traces .-> OTEL
```

## Phase 1 scope

Phase 1 delivers only the foundation:

- Workspace, modules, build/test/lint tooling (ADR-0001, ADR-0002).
- All `.proto` contracts, with Auth and Sync fully specified and the rest as
  stable signatures (ADR-0003).
- Generated Go code in `/gen/go`.
- Shared libraries in `/libs` (ADR-0005, ADR-0008, ADR-0009).
- Local infrastructure stack (compose) bootable with one command.
- This documentation and the ADR trail.

**Not in Phase 1:** any `/apps/*` service binary, DB migrations, frontend,
provider integrations. Those land in their respective phases per the
Generation Order.

## Request flow example: "user presses play"

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant S as Sync Service
    participant P as Playback Service
    participant K as Kafka
    participant O as Other Clients

    C->>GW: sync.Command(PLAY, fencing_token)
    GW->>S: forward (auth + role check)
    S->>S: validate fencing token, bump epoch
    S->>P: ApplySyncCommand(state)
    P-->>S: applied
    S-->>C: CommandResponse(epoch)
    S->>K: publish sync.updated.v1
    K-->>O: deliver snapshot
    O->>O: local correction via (media_time, wall_time)
```

The key invariants: the Sync Service owns the epoch; clients drop frames
older than their last-applied epoch; the fencing token prevents a
partitioned old host from corrupting state.
