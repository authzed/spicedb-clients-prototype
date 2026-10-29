import { full, atLeast, TransportSecurity } from "@spicedb/client";
import { TypedClient, TypedTransaction, Document, User, Team, Organization, Invitation, UserWildcard, Permission, subjectSetByName, type TeamMemberRef, type AnyResourceRef } from "./permissions";

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

// --- Adoption cases. Each line must fail on its own. ---
// creator admits only user:*, never a concrete user
// @ts-expect-error a wildcard-only relation rejects a concrete subject
Organization("x").creator(User("alice"));
// @ts-expect-error a concrete-only relation rejects the wildcard
Organization("x").admin(UserWildcard());
// grant admits only organization#can_admin, a permission userset
// @ts-expect-error grant rejects a user
Invitation("x").grant(User("alice"));
// @ts-expect-error grant rejects the organization itself
Invitation("x").grant(Organization("acme"));
// @ts-expect-error canAdmin stays the permission accessor; the subject accessor is canAdminSubject()
Invitation("x").grant(Organization("acme").canAdmin);
// @ts-expect-error timed_grant admits only the expiring userset
Invitation("x").timedGrant(Organization("acme").canAdminSubject());
// inviter admits only expiring users; its key admits only the base user
// @ts-expect-error inviter requires an expiration
Invitation("x").inviter(User("alice"));
// @ts-expect-error a key is identity-only and rejects an expiring subject
Invitation("x").inviterKey(User("alice").withExpiration(new Date()));
// @ts-expect-error a relation admitting only plain subjects has no key builder; its writer identifies the tuple
Organization("x").adminKey(User("alice"));
// @ts-expect-error writes cannot touch a key, only a full typed relationship
new TypedTransaction().touch(Invitation("x").inviterKey(User("alice")));
// @ts-expect-error the typed transaction does not shadow the native write
tc.write(new TypedTransaction());
// a relation check accepts only the relation's subjects
// @ts-expect-error organization#member checks a user, not a team member
tc.check(full(), Organization("x").memberRelation, Team("eng").member());
// @ts-expect-error a relation check accessor is not a writer
Organization("x").memberRelation(User("alice"));
// @ts-expect-error a permission of one resource type is not another's
const wrongType: Permission<"document", "view"> = Organization("x").view;
// @ts-expect-error relation and permission names are constants
const wrongName: "viewer" = Document("x").view.name;
// @ts-expect-error a subject set built by name is a validated AnySubject, not a specific ref type
const byNameSet: TeamMemberRef = subjectSetByName("team", "eng", "member");
// @ts-expect-error a subject set is not an object of any definition
const notAnObject: AnyResourceRef = Team("eng").member();
// @ts-expect-error lookupSubjectsByName takes a runtime subject type name, not a factory
tc.lookupSubjectsByName(full(), "document", "x", "view", User);
// @ts-expect-error checkByName takes a generated subject, not a tuple string
tc.checkByName(full(), "document", "x", "view", "user:alice");
