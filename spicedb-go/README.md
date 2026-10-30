# spicedb-go

The idiomatic Go client for [SpiceDB](https://authzed.com/spicedb). Requests
and results are native Go values with `iter.Seq2` streams, checks fail closed,
retries replay only idempotent work, and `client/clienttest` provides an
in-memory test double. Pair it with `spicedb-gen` for a schema-typed layer.

## Installation

The module is not tagged yet. Depend on a commit of this repository, or use a
`replace` directive pointing at a checkout:

```bash
go get github.com/authzed/spicedb-clients/spicedb-go@<commit>
```

```text
// go.mod, with a local checkout
replace github.com/authzed/spicedb-clients/spicedb-go => ../spicedb-clients/spicedb-go
replace github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto => ../spicedb-clients/proto-clients/spicedb-go-proto
```

Requirements: Go 1.26, SpiceDB 1.30 or later.

## First call

Start a local SpiceDB (`docker run --rm -p 50051:50051 authzed/spicedb serve
--grpc-preshared-key somerandomkeyhere`), then:

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/authzed/spicedb-clients/spicedb-go/client"
    "github.com/authzed/spicedb-clients/spicedb-go/consistency"
    "github.com/authzed/spicedb-clients/spicedb-go/rel"
)

func main() {
    ctx := context.Background()
    c, err := client.NewPlaintext("localhost:50051", "somerandomkeyhere")
    if err != nil {
        log.Fatal(err)
    }
    defer func() { _ = c.Close() }()
    if err := c.Ready(ctx, client.WithReadyTimeout(5*time.Second)); err != nil {
        log.Fatal(err) // unreachable endpoint: fail at startup
    }

    if _, err := c.WriteSchema(ctx, client.WriteSchemaRequest{Schema: `
definition user {}
definition document {
    relation viewer: user
    permission view = viewer
}`}); err != nil {
        log.Fatal(err)
    }

    var txn rel.Txn
    if err := txn.Touch(rel.MustFromTuple("document:readme#viewer@user:alice")); err != nil {
        log.Fatal(err)
    }
    written, err := c.Write(ctx, client.WriteRequest{Transaction: txn})
    if err != nil {
        log.Fatal(err)
    }

    result, err := c.Check(ctx, client.PermissionCheck{
        Resource:    rel.MustNewObject("document", "readme"),
        Permission:  "view",
        Subject:     rel.MustNewSubject(rel.MustNewObject("user", "alice"), ""),
        Consistency: consistency.AtLeast(written.Revision),
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("alice can view:", result.HasPermission()) // only an unconditional grant is true
}
```

Remote plaintext needs an explicit opt-in; `NewSystemTLS` is the default for
remote endpoints. Endpoints may be `host:port`, `http://host:port` or
`https://host[:port]`, and `dns:///` and `unix:` targets are supported.

## Testing without a server

`clienttest.NewStore(...)` implements `client.Interface`, the full client
surface, so code written against the interface runs on the double or the real
client. It records every call, accepts fault injection, resolves checks from
registered results, a resolver callback (`store.SetCheckResolver(clienttest.GrantAll)`)
or stored relationships, and serves lookups.

The double does not evaluate your schema: a computed permission such as
`view = reader + writer + parent->read` is granted only by a stored
relationship named `view` or by your resolver. Projects whose permissions are
all computed get little from stored-relationship grants; model them with the
resolver, or test the permission logic against SpiceDB itself
(`spicedb serve-testing` or a container).

## Further reading

- [DESIGN.md](DESIGN.md): the Go client's design and its rules.
- [MIGRATION.md](MIGRATION.md): moving from authzed-go or gochugaru.
- [AI_INTEGRATION_GUIDE.md](AI_INTEGRATION_GUIDE.md): which tier to use and how to integrate.
- [examples/](examples/): runnable examples that double as integration tests.
