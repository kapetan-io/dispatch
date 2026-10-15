# Surface testing examples

One way to apply the ideas in `SKILL.md`, not the way. The skill states the rules. These examples show Go and Kotlin shapes for them. Take what helps, ignore the rest, and prefer your own approach where you can do better, since the names and helper functions here (`api.NewServer`, `containsOrder`, `jobStatus`) are invented for illustration.

## Go CLI with a thin `main`

`main` only calls `Run`, so tests enter the same way the binary does.

```go
// main.go
package main

import (
    "context"
    "os"
    "github.com/your/project/cmd"
)

func main() {
    os.Exit(cmd.Run(context.Background(), os.Args[1:], cmd.RunOptions{
        Stdout: os.Stdout,
        Stderr: os.Stderr,
    }))
}
```

The `Run()` function accepts arguments and injected dependencies. This IS the surface — both `main()` and tests enter through it:

```go
// cmd/run.go
package cmd

type RunOptions struct {
    Stdout io.Writer
    Stderr io.Writer
}

func Run(ctx context.Context, args []string, opts RunOptions) int {
    // All application logic lives here
}
```

Tests call `Run()` with test arguments and capture output:

```go
func TestSubCommand(t *testing.T) {
    var stdout bytes.Buffer
    exitCode := cmd.Run(context.Background(), []string{"sub-command", "-f", "filename.ext"}, cmd.RunOptions{
        Stdout: &stdout,
    })
    require.Equal(t, 0, exitCode)
    assert.Contains(t, stdout.String(), "expected output")
}
```

## Go HTTP service

Start a real server and send real requests.

```go
func TestCreateUser(t *testing.T) {
    // Given: a running server
    server := api.NewServer(api.ServerOptions{ /* inject real or test deps */ })
    go server.Start("localhost:0")
    defer server.Shutdown()

    // When: creating a user via HTTP
    resp, err := http.Post(server.URL()+"/users", "application/json",
        strings.NewReader(`{"name":"Alice"}`))
    require.NoError(t, err)
    defer resp.Body.Close()

    // Then: the response confirms creation
    assert.Equal(t, http.StatusCreated, resp.StatusCode)
}
```

## Kotlin/Micronaut, mocking internals versus booting the application

The first test breaks on any refactor even when API behavior is unchanged. The second enters through HTTP.

```kotlin
// DON'T DO THIS
class UserServiceTest {
    private val repository = mockk<UserRepository>()
    private val service = UserService(repository)

    @Test fun `should create user`() {
        every { repository.save(any()) } returns User("Alice")
        val result = service.createUser("Alice")
        assertEquals("Alice", result.name)
        verify { repository.save(any()) }
    }
}
```

**Right approach** — boot the full application via `@MicronautTest` and go through HTTP:

```kotlin
@MicronautTest
class UserControllerTest {
    @Inject lateinit var client: HttpClient

    @Test fun `should create user`() {
        val response = client.toBlocking().exchange(
            HttpRequest.POST("/users", mapOf("name" to "Alice")),
            Map::class.java)
        assertEquals(HttpStatus.CREATED, response.status)
    }
}
```

`@MicronautTest` starts the full application context. The test enters through the same HTTP endpoint a real client would use. Use `@MockBean` or `@Replaces` ONLY for external boundaries (third-party APIs, payment gateways) — never for internal services.

## Asserting a downstream effect of asynchronous work

The order is accepted asynchronously, and the test polls the billing endpoint.

```go
func TestOrderProcessing(t *testing.T) {
    // Given: a running server
    server := api.NewServer(api.ServerOptions{})
    go server.Start("localhost:0")
    defer server.Shutdown()

    // When: submitting an order
    resp, err := http.Post(server.URL()+"/orders", "application/json",
        strings.NewReader(`{"item":"widget","qty":5}`))
    require.NoError(t, err)
    assert.Equal(t, http.StatusAccepted, resp.StatusCode)

    // Then: the order appears in the billing system
    require.Eventually(t, func() bool {
        bills, _ := http.Get(server.URL() + "/billing/pending")
        // parse and check for our order
        return containsOrder(bills, "widget", 5)
    }, time.Second, 10*time.Millisecond)
}
```

The async work resulted in a billing record. That's observable through the public interface. No special APIs needed — just poll the downstream result.

## Embedded real service

The dependency runs in-process, and only its own externals are substituted.

func TestOrderWithPayments(t *testing.T) {
    // Given: the payments service running embedded — real code, real handlers
    payments := paymentssvc.NewEmbedded(paymentssvc.EmbeddedOptions{
        // payments' own external deps get substituted here, recursively
    })
    defer payments.Shutdown()

    // Given: the order service wired to the embedded payments service
    server := api.NewServer(api.ServerOptions{
        PaymentsURL: payments.URL(),
    })
    go server.Start("localhost:0")
    defer server.Shutdown()

    // When/Then: exercise through the Order surface — real payments code runs
}
```

This is higher fidelity than any fake: the *real* payments code runs, using its real business logic, real HTTP handlers, and real wire protocol. You only substitute the payments service's own external dependencies (recursively applying the same rules).

## In-process S3 fake

`gofakes3` speaks the real S3 protocol, so the client, codec and connection handling all run.

func TestImageUpload(t *testing.T) {
    // Given: an in-process S3 fake
    backend := s3mem.New()
    s3srv := httptest.NewServer(gofakes3.New(backend).Server())
    defer s3srv.Close()

    // Given: a running server wired to the fake
    server := api.NewServer(api.ServerOptions{
        S3Endpoint: s3srv.URL,
        S3Bucket:   "images",
    })
    go server.Start("localhost:0")
    defer server.Shutdown()

    // When: uploading an image through the HTTP surface
    resp, err := http.Post(server.URL()+"/images", "image/png", bytes.NewReader(pngBytes))
    require.NoError(t, err)
    assert.Equal(t, http.StatusCreated, resp.StatusCode)

    // Then: the object exists in S3 — verified via the real S3 protocol
    obj, err := newS3Client(s3srv.URL).GetObject(ctx, &s3.GetObjectInput{
        Bucket: aws.String("images"),
        Key:    aws.String("expected-key"),
    })
    require.NoError(t, err)
    require.NotNil(t, obj)
}
```

## Published fake in a container

Same test shape as the in-process fake, with a container for the endpoint.

container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
    ContainerRequest: testcontainers.ContainerRequest{
        Image:        "sculley/fake-s3",
        ExposedPorts: []string{"4569/tcp"},
        WaitingFor:   wait.ForListeningPort("4569/tcp"),
    },
    Started: true,
})
require.NoError(t, err)
defer container.Terminate(ctx)

endpoint, _ := container.Endpoint(ctx, "http")
server := api.NewServer(api.ServerOptions{S3Endpoint: endpoint})
// ...exercise the surface and assert the downstream S3 result as before.
```

## Kotlin substitute at the DI boundary

`@MockBean` replaces an external dependency. The application still boots, and internal services run for real. `FakeEventBroker` stands for an in-memory fake that refuses what the real broker refuses, as `SKILL.md` describes under "A fake you write". It is not a recorder that accepts every call.

```kotlin
@MicronautTest
class OrderControllerTest {
    @Inject lateinit var client: HttpClient
    @Inject lateinit var fakeBroker: FakeEventBroker

    @Test fun `should emit order event when order is submitted`() {
        val response = client.toBlocking().exchange(
            HttpRequest.POST("/orders", mapOf("item" to "widget", "qty" to 5)),
            Map::class.java)
        assertEquals(HttpStatus.ACCEPTED, response.status)

        await().atMost(Duration.ofSeconds(1)).untilAsserted {
            val events = fakeBroker.events()
            assertEquals(1, events.size)
            assertEquals("order.created", events[0].type)
        }
    }

    @MockBean(EventBroker::class) fun eventBroker(): EventBroker = FakeEventBroker()
}
```

`@MockBean` here replaces an *external* dependency — the event broker is a system boundary. The full application context still boots, the request still goes through HTTP, and all internal services run for real. `await().atMost()` (Awaitility) is the Kotlin equivalent of Go's `require.Eventually`.

**Never use `@MockBean` for internal components.** `@MockBean(UserService::class)` or `@MockBean(OrderRepository::class)` means you are testing implementation, not behavior. Either `@MockBean` or `@Replaces` works — the rule is the same: only substitute *external* boundaries.

## Frozen clock

A provider you freeze, inject through the options struct and advance.

func TestRetryAfterBackoff(t *testing.T) {
    // Given: a frozen clock provider to inject into the service
    clk := clock.NewProvider()
    clk.Freeze(time.Now())
    defer clk.UnFreeze()

    // Given: a running server with the clock injected
    server := api.NewServer(api.ServerOptions{Clock: clk})
    go server.Start("localhost:0")
    defer server.Shutdown()

    // When: kicking off an operation that retries after 30s
    resp, err := http.Post(server.URL()+"/jobs", "application/json", strings.NewReader(`{}`))
    require.NoError(t, err)
    assert.Equal(t, http.StatusAccepted, resp.StatusCode)

    // Then: advancing past the backoff fires the retry
    clk.Advance(31 * time.Second)
    require.Eventually(t, func() bool {
        return jobStatus(server) == "retried"
    }, time.Second, 10*time.Millisecond)
}
```

## Observability API for work the surface can't show

`Stats()` is useful to an operator too, so it isn't test-only code.

type Stats struct {
    WALWriteCount     int64
    WALLastWriteTime  time.Time
    DirtyPages        int64
    PagesFlushedToWAL int64
    PagesFlushedToDB  int64
}

func (db *DB) Stats() Stats {
    return db.stats.snapshot()
}
```

```go
func TestWALPeriodicWrite(t *testing.T) {
    db := NewDB(DBOptions{WALFlushInterval: 100 * time.Millisecond})
    defer db.Close()

    require.NoError(t, db.Insert("key", "value"))

    require.Eventually(t, func() bool {
        s := db.Stats()
        return s.WALWriteCount > 0 && s.DirtyPages == 0
    }, time.Second, 10*time.Millisecond)
    assert.Greater(t, db.Stats().PagesFlushedToWAL, int64(0))
}
```

## Repository with a real adapter and an in-memory fake

Both implementations satisfy one interface. Contract tests keep them behaving the same.

type OrderRepository interface {
    Save(ctx context.Context, order Order) error
    Get(ctx context.Context, id string) (Order, error)
    ListPending(ctx context.Context) ([]Order, error)
}

type PostgresOrderRepository struct { /* real adapter */ }
type InMemoryOrderRepository struct { /* map-backed fake */ }
```

## Tests derived from correctness claims

An invariant violation, a behavioral constraint under a fault, and a boundary precondition. `FakeEventBroker` in the second test stands for an in-memory fake that refuses what the real broker refuses, as `SKILL.md` describes under "A fake you write". It is not a recorder that accepts every call.

```go
func TestTransferRejectsNegativeBalance(t *testing.T) {
    server := api.NewServer(api.ServerOptions{})
    go server.Start("localhost:0")
    defer server.Shutdown()

    // Given: an account with balance 100
    createAccount(t, server, "acct-1", 100)

    // When: attempting a transfer that would make the balance negative
    resp, err := http.Post(server.URL()+"/transfers", "application/json",
        strings.NewReader(`{"from":"acct-1","to":"acct-2","amount":150}`))
    require.NoError(t, err)

    // Then: the transfer is rejected and the balance is unchanged
    assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
    assert.Equal(t, 100, getBalance(t, server, "acct-1"))
}
```

The spec says "Transfer preserves the non-negative-balance invariant because it checks balance atomically." This test proves that argument holds in the actual implementation. One test per invariant per operation that touches it.

### Behavioral Constraint Tests

For each behavioral constraint, write a test that verifies enforcement — often by injecting a fault at a dependency boundary and observing how the system responds.

```go
func TestMessageNeverSilentlyDropped(t *testing.T) {
    fakeBroker := &FakeEventBroker{FailNext: true}
    server := api.NewServer(api.ServerOptions{EventBroker: fakeBroker})
    go server.Start("localhost:0")
    defer server.Shutdown()

    // When: submitting an order while the broker is failing
    resp, err := http.Post(server.URL()+"/orders", "application/json",
        strings.NewReader(`{"item":"widget"}`))
    require.NoError(t, err)

    // Then: the system surfaces the failure rather than silently dropping
    assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}
```

### Boundary Contract Tests

For each component boundary with explicit preconditions/postconditions in the spec, write a test that violates a precondition and verifies the violation is rejected cleanly.

```go
func TestUnauthenticatedRequestRejected(t *testing.T) {
    server := api.NewServer(api.ServerOptions{})
    go server.Start("localhost:0")
    defer server.Shutdown()

    // When: calling a protected endpoint without authentication (violating precondition)
    resp, err := http.Get(server.URL() + "/admin/users")
    require.NoError(t, err)

    // Then: the precondition violation is rejected with a clear error
    assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
```

## Surface testing and integration testing

They overlap but differ in intent. **Integration testing** asks "do these components work together correctly?" — focus on the seams (Service A↔B, schema↔ORM). **Surface testing** asks "does the system behave correctly when used as intended?" — focus on observable behavior from the outside.

Surface tests boot the full application, so internal components are integrating — but integration is a side effect, not the goal. Surface tests also explicitly fake external boundaries to stay fast, deterministic, and focused on *this* system's behavior; integration tests typically wire real things together. Complementary, not interchangeable.

**Internal library boundaries** sit at the intersection of both: a domain-complete package within the codebase defines its own surface and warrants its own surface tests, independent of the systems that consume it. When integration tests wire two components together, an internal library boundary is where you draw the line — it is a first-class surface, not an internal seam to cut through.
