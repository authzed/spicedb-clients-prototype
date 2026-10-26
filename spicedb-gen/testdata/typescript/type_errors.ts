import { full, atLeast, TransportSecurity } from "@spicedb/client";
import { TypedClient, Document, User, Team } from "./permissions";

declare const tc: TypedClient;

// @ts-expect-error: edit only reachable by user, not team#member
tc.check(full(), Document("x").edit, Team("eng").member());

// @ts-expect-error: delete only reachable by user
tc.check(full(), Document("x").delete, Team("eng").member());

// @ts-expect-error: editor relation only allows user
Document("x").editor(Team("eng"));

// @ts-expect-error: owner relation only allows user
Document("x").owner(Team("eng").member());

// @ts-expect-error: editor relation does not accept caveated user
Document("x").editor(User("alice").withIpRange({ allowedCidr: "10.0.0.0/8" }));

// @ts-expect-error: withIpRange requires a context object
User("alice").withIpRange();

// @ts-expect-error: wrong caveat context type (IpRangeContext has allowedCidr, not start/end)
User("alice").withIpRange({ start: "2024-01-01", end: "2024-12-31" });

// @ts-expect-error: persisted revisions must be explicitly constructed as ZedToken.
atLeast("stored-revision");
// @ts-expect-error: tracing requires a discoverable enum choice.
tc.check(full(), Document("x").view, User("alice"), { tracing: true });
// @ts-expect-error: remote credential policy is not a boolean.
TypedClient.create("remote.example:50051", "secret", { transportSecurity: TransportSecurity.Plaintext, remoteCredentials: true });

// @ts-expect-error Lookup filtering uses a named mode, never a boolean.
tc.lookupResources(full(), Document.view, User("alice"), { permissionshipFilter: true });

// Write helpers return a wrapper; consistency requires its native revision.
declare const written: Awaited<ReturnType<TypedClient["touch"]>>;
atLeast(written.revision);
// @ts-expect-error A WriteResult is not a revision token.
atLeast(written);

// @ts-expect-error Bounded deletion still requires a limit.
tc.deleteRelationships({ resourceType: "document" }, {});
// @ts-expect-error Watch filters use native typed filter fields.
tc.watch({ relationshipFilters: [{ resourceType: 42 }] });
