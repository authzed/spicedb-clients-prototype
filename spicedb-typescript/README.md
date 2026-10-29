# @spicedb/client

The idiomatic TypeScript client for [SpiceDB](https://authzed.com/spicedb): native
request and result objects, required consistency, typed errors, paged async
iterators, retries for idempotent reads, and an in-memory test double. Pair it
with `spicedb-gen --lang typescript` for a schema-checked typed client.

> **Prototype.** This client is unreleased; its API may change. Pin a commit.

## Requirements

- Node.js 20.19 or later. The package is ESM; Node 20.19+ also lets CommonJS
  code `require()` it.
- SpiceDB 1.30 or later.
- Runtime dependencies (installed for you): `@bufbuild/protobuf` ^2.11.0,
  `@connectrpc/connect` ^2.0.0, and `@spicedb/proto` (the generated protocol
  stubs, built from this repository). `@connectrpc/connect-node` provides the
  HTTP/2 transport. `@grpc/grpc-js` is not used.

## Installation

Once published:

```bash
npm install @spicedb/client
```

### Before publication

`@spicedb/client` depends on `@spicedb/proto`, which is also built from this
repository. Build both and pack them as tarballs:

```bash
git clone https://github.com/authzed/spicedb-clients-prototype && cd spicedb-clients-prototype
pnpm install
(cd proto-clients/spicedb-typescript-proto && pnpm build && pnpm pack --pack-destination /tmp/spicedb-ts)
(cd spicedb-typescript && pnpm build && pnpm pack --pack-destination /tmp/spicedb-ts)
```

Then install both tarballs together in your project, so the client's
`@spicedb/proto` dependency resolves to the local one:

```bash
npm install /tmp/spicedb-ts/spicedb-proto-0.1.0.tgz /tmp/spicedb-ts/spicedb-client-0.1.0.tgz
```

Inside a pnpm workspace that contains this repository, depend on
`"@spicedb/client": "workspace:*"` instead.

Both packages are ESM and load with Node's own loader, as well as through
bundlers and TypeScript-aware loaders such as `tsx`.

## First call

Start a local SpiceDB (`docker run --rm -p 50051:50051 authzed/spicedb serve
--grpc-preshared-key somerandomkeyhere`), then:

```ts
import { createSpiceDBClient, TransportSecurity, Transaction, relationship, full } from "@spicedb/client";

const client = createSpiceDBClient("localhost:50051", "somerandomkeyhere", {
    transportSecurity: TransportSecurity.Plaintext, // loopback only; TLS is the default
});

await client.writeSchema(`
definition user {}
definition document {
    relation viewer: user
    permission view = viewer
}`);

const { revision } = await client.write(new Transaction()
    .touch(relationship("document:readme", "viewer", "user:alice")));

const result = await client.check({
    resource: { objectType: "document", objectId: "readme" },
    permission: "view",
    subject: { object: { objectType: "user", objectId: "alice" } },
    consistency: full(), // or atLeast(revision)
});
console.log(result.hasPermission()); // true only for an unconditional grant

client.close();
```

In tests, use `InMemorySpiceDB` (exported from the same module) wherever a
`SpiceDBClientInterface` is accepted, including a generated `TypedClient`.

## Learn more

- [AI_INTEGRATION_GUIDE.md](AI_INTEGRATION_GUIDE.md): the typed client, safe
  patterns for checks, writes, reads, errors and tests.
- [MIGRATION.md](MIGRATION.md): moving from `@authzed/authzed-node`.
- [DESIGN.md](DESIGN.md): the TypeScript API's design, applying the
  [repository-wide design](../DESIGN.md).
- [examples/](examples/): runnable examples, executed against a live SpiceDB in CI.
- [CHANGELOG.md](CHANGELOG.md).
