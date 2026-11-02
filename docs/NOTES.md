# Go Architectural Patterns, Capabilities, and Engineering Notes

This document captures core architectural patterns, idiomatic translations from C#/.NET and TypeScript/React, high-yield use cases, hard boundaries/limitations, the polyglot language decision matrix, and the blueprint for the upcoming `golang-service-daemon` toolbelt archetype.

---

## 1. Translating Enterprise SOLID & React Rigor to Idiomatic Go

Coming from deep mastery in **C# (strict SOLID, DI containers, class hierarchies)** and **TypeScript/React (Zustand immutable slices, reactive subscriptions, atomic re-renders)**, Go requires a translation of principles rather than an abandonment of them.

### SOLID in Idiomatic Go

| Principle | In C# / OOP | In Idiomatic Go |
| :--- | :--- | :--- |
| **S** (Single Responsibility) | One class, one responsibility. | **Small, focused packages and interfaces**. Packages are named strictly by what they provide (`db`, `mcpserver`, `config`, `pipeline`), never generic catch-alls like `utils` or `common`. |
| **O** (Open/Closed) | Subclassing, abstract classes, virtual method overrides. | **Embedding (Composition) & Higher-Order Functions**. Structs embed other structs or accept interface adapters without mutating base behavior. |
| **L** (Liskov Substitution) | Strict subtype inheritance hierarchies. | **Structural (Implicit) Subtyping**. Any struct implementing the method signatures satisfies the interface automatically—no `implements` keyword required. |
| **I** (Interface Segregation) | Fat interfaces split into role interfaces. | **Micro-interfaces ($\le 2$ methods)**. Modeled after the standard library (`io.Reader`, `io.Closer`). Defined at the *consumer* call site, not the producer. Rule: *"Accept interfaces, return structs"*. |
| **D** (Dependency Inversion) | Heavy IoC/DI reflection containers (Autofac, Microsoft.Extensions.DependencyInjection). | **Explicit Constructor Injection via Structs or Functional Options**. No reflection magic at startup; wire dependencies explicitly in `main.go`. |

---

## 2. Concurrency & State Management: Channel Actors vs `sync.RWMutex`

Go's famous mantra: *"Do not communicate by sharing memory; instead, share memory by communicating."* However, senior engineering requires picking the right primitive for the right problem:

```
                  State Access Frequency & Complexity
                                    │
          High-Read In-Memory Cache │ Concurrent Processing Loop
          (Key-Value, Routing Table)│ (Debouncing, Event Filtering)
                                    ▼
                         ┌───────────────────────┐
                         │   sync.RWMutex        │
                         │   State Container     │
                         └───────────────────────┘
                                    │
                                    ▼
                         ┌───────────────────────┐
                         │   Channel Actor       │
                         │   Single Goroutine    │
                         └───────────────────────┘
```

### Pattern A: Read-Heavy Concurrent State (`sync.RWMutex`)
Use when state is queried frequently across multiple goroutines and mutations are simple key-value or slice updates:
```go
type SessionCache struct {
    mu       sync.RWMutex
    sessions map[string]*Session
}

func (c *SessionCache) Get(id string) (*Session, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    sess, ok := c.sessions[id]
    return sess, ok
}

func (c *SessionCache) Set(sess *Session) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.sessions[sess.ID] = sess
}
```

### Pattern B: Event-Driven Debouncing & Ingestion (Channel Actor Loop)
Use when sequential state mutations, time-delayed actions, and coordination between async workers are required (e.g. `gmessages-rag` 30-minute idle debouncer):
```go
type IngestionManager struct {
    incomingCh chan *gm.Message
    stopCh     chan struct{}
}

func (m *IngestionManager) Start(ctx context.Context) {
    go func() {
        for {
            select {
            case msg := <-m.incomingCh:
                m.handleMessage(ctx, msg)
            case <-ctx.Done():
                return
            }
        }
    }()
}
```

---

## 3. Construction: Functional Options Pattern

Avoid telescopic constructors (`NewServer(port, host, timeout, retry, buffer)`) or builder classes. Use the idiomatic **Functional Options Pattern**:

```go
type Server struct {
    port              int
    enableSendMessage bool
    timeout           time.Duration
}

type Option func(*Server)

func WithPort(port int) Option {
    return func(s *Server) { s.port = port }
}

func WithSendMessage(enable bool) Option {
    return func(s *Server) { s.enableSendMessage = enable }
}

func NewServer(opts ...Option) *Server {
    s := &Server{
        port:    8090, // Defaults
        timeout: 30 * time.Second,
    }
    for _, opt := range opts {
        opt(s)
    }
    return s
}
```

---

## 4. Context Propagation & Error Wrapping

1. **Context First**: Every network, database, or long-running function accepts `ctx context.Context` as its first parameter to enable cascading timeouts and clean cancellation.
2. **Explicit Error Wrapping**: Errors are values, never caught exceptions. Always wrap errors with context using `%w`:
   ```go
   if err := db.Ping(ctx); err != nil {
       return fmt.Errorf("database connectivity check failed: %w", err)
   }
   ```

---

## 5. Ideal Use Cases for Go Applications

Go shines brightest when building resilient network infrastructure, high-throughput daemons, and developer tooling:

1. **High-Throughput Concurrent Network Daemons & Services**:
   - Web servers, API gateways, reverse proxies, and microservices (e.g., Caddy, Traefik, Docker, Kubernetes, CockroachDB).
   - Go's `M:N` work-stealing scheduler multiplexes hundreds of thousands of concurrent goroutines over OS threads. Goroutines initialize with ~2-4 KB of memory (vs 1-2 MB per OS thread in Java/C#).
   - Standard library `net/http` is production-ready, supporting HTTP/1.1, HTTP/2, and HTTP/3 without external server runtimes.

2. **Real-Time Stream Processing & Ingestion Pipelines**:
   - Long-polling daemon bridges, WebSocket message hubs, and protocol decoders (e.g., `gmessages-rag` pairing loop with Google Messages).
   - First-class channel primitives (`select`, buffered channels, timers) make fan-in/fan-out, sliding-window debouncing, and rate limiting trivial to write and reason about.

3. **CLI Tools & Operational Infrastructure**:
   - Systems tooling (e.g., `kubectl`, `terraform`, `gh`, `docker`, `hugo`, `fzf`).
   - Compiles to a **single, dependency-free static binary** (`CGO_ENABLED=0`).
   - Frictionless cross-compilation across platforms (`GOOS=linux GOARCH=arm64 go build`).
   - Sub-10ms cold start time with zero JIT or runtime interpreter overhead.

4. **Agent-Native Systems & MCP Servers (Model Context Protocol)**:
   - Ultra-low baseline memory (<15-30 MB RAM vs 200+ MB for Node/.NET/Python).
   - Seamless multiplexing across stdio, Server-Sent Events (SSE), and Streamable HTTP endpoints.
   - Strict JSON serialization and fast validation.

5. **Cloud-Native Infrastructure & Storage Middleware**:
   - Distributed key-value stores (etcd, Consul), consensus engines (Raft), and distributed storage systems (MinIO).

---

## 6. What Go Cannot Do / Where Go Falls Short

Understanding Go's architectural non-goals prevents misapplying it to domains where other languages excel:

| Limitation / Non-Goal | Why Go Fails or Struggles | Preferred Alternative |
| :--- | :--- | :--- |
| **Hard Real-Time & Deterministic Systems** | Go's garbage collector operates concurrently with sub-millisecond pauses, but it is **not deterministic**. Unsuitable for avionics, medical devices, automotive ECUs, or ultra-low-latency HFT trading engines. | **Rust**, **C**, **C++** |
| **Dynamic Metaprogramming & Runtime `eval`** | Go is strictly compiled and statically typed. There is no `eval()`, no monkey-patching, and runtime plugin loading (`plugin` package) is notoriously brittle and platform-constrained. | **Python**, **TypeScript / Node.js** |
| **Complex OOP Inheritance Trees** | Go rejects `extends`, `abstract`, and virtual method tables. Attempting to model deep enterprise domain taxonomies via struct embedding leads to unwieldy boilerplate. | **C# (.NET)**, **Java** |
| **Native Cross-Platform GUI Desktop Apps** | Go has no official GUI framework. UI libraries (Fyne, Gio) or Webview frameworks (Wails) rely on CGO bindings, breaking clean cross-compilation and native platform UI polish. | **TypeScript (Tauri/Electron)**, **C# (WPF/MAUI/Avalonia)**, **Swift/Kotlin** |
| **Scientific Computing & ML Model Training** | The ML/DL ecosystem (PyTorch, JAX, CUDA kernels, HuggingFace) is deeply rooted in Python and C++. Go lacks operator overloading, tensor primitives, and interactive REPL/notebook tooling. | **Python**, **C++ / CUDA** |
| **Advanced Generic / Type-Level Metaprogramming** | While Go has basic type parameters (generics since 1.18), it lacks higher-kinded types, template metaprogramming (`constexpr`), or full algebraic data types (enums with pattern matching). | **Rust**, **Haskell**, **Scala** |

---

## 7. Polyglot Language Decision Framework

To prevent arbitrary language selection, developers and AI agents must evaluate project requirements against the following deterministic matrix:

### Polyglot Evaluation Matrix

| Metric / Dimension | Go (1.24+) | C# (.NET 10) | Python (3.12+) | TypeScript / React | Modern C++ (C++20/23) |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Primary Sweet Spot** | Network daemons, streaming ETL, CLIs, agent MCP services | Enterprise systems, fullstack control, rich domain logic | AI/ML pipelines, rapid prototypes, data analysis | Interactive web UIs, visualization, design systems | Hard real-time, low-latency DSP, graphics/simulation |
| **Concurrency Model** | Goroutines + Channels ($M:N$, work-stealing) | `async`/`await` Task Parallel Library, ThreadPool | `asyncio` (Single-threaded event loop), Multiprocessing | Browser/Node Event Loop (`async`/`await`, Web Workers) | OS Threads, `std::jthread`, Lock-free atomics, Coroutines |
| **Memory Footprint** | Extremely low (10–30 MB) | Medium (50–150 MB) | High (80–250 MB) | Medium–High (Node: 60–200 MB; Browser: DOM-dependent) | Minimal / Deterministic (Stack & custom arenas) |
| **Binary Deployment** | Single static executable (`CGO_ENABLED=0`) | Single-file executable (Native AOT) or framework-dependent | Virtualenv / Wheel / Docker image | Node bundles, Static assets (HTML/JS/CSS) | Native binary linked dynamically or statically |
| **Cold Start Time** | < 10 ms | 15–50 ms (Native AOT) | 100–500 ms (interpreter imports) | 50–150 ms (Node V8) | < 5 ms |
| **Sub-millisecond Latency** | Soft real-time (GC pauses < 1ms) | Soft real-time (Generational GC) | Not suitable (GIL + GC pauses) | Not suitable (GC + Single-threaded loop) | **Hard deterministic real-time (Zero GC)** |
| **Dynamic Capabilities** | Compile-time static | Reflection, Roslyn runtime emit | Dynamic `eval`, monkey-patching, dynamic typing | Dynamic types, runtime evaluation | Compile-time templates, `constexpr` |
| **Ecosystem Maturity** | Cloud infrastructure, networking, Kubernetes, Docker | Enterprise, Windows, Azure, game dev (Unity), finance | Machine learning, LLM tooling, scientific computing | Web standards, UI components, browsers, node tooling | Systems, game engines, physics, CAD, embedded devices |

### Deterministic Language Decision Flowchart

```
Start
  │
  ├─► Is this primarily an interactive Web or Desktop UI?
  │     └─► YES ──► React + TypeScript (Vite / Next.js)
  │
  ├─► Does the task require hard real-time determinism (zero GC pauses), embedded hardware, or custom SIMD/GPU physics?
  │     └─► YES ──► Modern C++ (C++20/23 with strict RAII)
  │
  ├─► Is the primary focus ML model training, computer vision research, PyTorch/JAX pipelines, or rapid exploratory scripts?
  │     └─► YES ──► Python (FastAPI + FastMCP + uv)
  │
  ├─► Is this an enterprise business system with complex relational entity models, deep domain-driven design, or fullstack .NET integration?
  │     └─► YES ──► C# (.NET 10 with Dapper/EF Core + React UI)
  │
  └─► Is it a high-throughput network daemon, concurrent protocol gateway, real-time streaming pipeline, single-binary CLI, or agent MCP server?
        └─► YES ──► Go (1.24+ with Goroutines, pgxpool, and Functional Options)
```

---

## 8. Proposed Toolbelt Archetype: `golang-service-daemon`

When adding the Go archetype to `AgenticEngineeringToolbelt`, the following specification and file layout will be codified:

### Target Project Layout (`Standard Go Project Layout`)
```
├── cmd/
│   └── <service-name>/
│       └── main.go                  # Entry point: signals, configuration, wire dependencies
├── internal/
│   ├── config/                      # Strongly typed environment configuration (godotenv, envconfig)
│   ├── db/                          # Database connection pool (pgxpool), migrations, idempotent CRUD
│   │   └── migrations/              # Embedded .sql migration scripts (//go:embed)
│   ├── <domain>/                    # Pure business logic, state machines, channel actor loops
│   └── mcpserver/                   # Agent-native MCP server (Streamable HTTP, SSE, stdio)
├── docs/
│   ├── ARCHITECTURE.md              # Living architecture, dataflow diagrams, and decisions
│   ├── REQUIREMENTS.md              # Traceable requirement IDs (e.g. AUTH-01, DB-02)
│   └── CHANGELOG.md                 # Keep a Changelog format
├── Makefile                         # Canonical tasks: build, test, lint, test-race, run
├── Dockerfile                       # Multi-stage build (golang:1.24-alpine -> scratch/alpine:3.21)
├── docker-compose.yml               # Local dependencies (Postgres 17, pgvector, etc.)
├── .golangci.yml                    # Strict linting rules (errcheck, gosec, revive, staticcheck)
├── go.mod
└── go.sum
```

### Verification & Anti-Test Theatre Standards for Go
1. **Zero Mock Framework Theatre**: Reject fragile mock generation frameworks (`mockgen`, `testify/mock`) that verify calls rather than outcomes. Use real ephemeral database containers or in-memory interface implementations.
2. **Table-Driven Tests**: Write idiomatic Go table tests exercising `0-1-N` boundaries, boundary edge cases, and error branches.
3. **Mandatory Concurrency Race Detection**: Every CI/CD pipeline and local test run must execute with `go test -race ./...`.
4. **Context Timeout Enforcement**: All tests must supply bounded contexts (`context.WithTimeout(ctx, 2*time.Second)`) to prevent hung test suites.
5. **Code Coverage Threshold**: Target $>85\%$ branch coverage across all business logic in `internal/`.

---

## 9. Toolbelt Integration Roadmap

To integrate Go into `AgenticEngineeringToolbelt`:
1. **File Issue on `spelech/AgenticEngineeringToolbelt`**: Detail archetype requirements, language decision matrix, and scaffolding automation.
2. **Add `archetypes/golang-service-daemon.md`**: Complete blueprint following the archetype template.
3. **Update `standards/ENGINEERING_STYLE_GUIDE.md`**: Embed the Polyglot Language Decision Matrix and Go coding standards.
4. **Update `standards/TESTING_HARNESS_PATTERNS.md`**: Codify Go table-driven tests, race detection, and ephemeral container patterns.
5. **Update `.agents/skills/scaffold-project`**: Enable automated scaffolding of Go daemons with embedded migrations, config loader, and Dockerfiles.
6. **Update `.agents/skills/code-review-foci`**: Include Go-specific reviewer checklists (context propagation, error wrapping `%w`, race detector cleanliness, `0600` file permissions).
