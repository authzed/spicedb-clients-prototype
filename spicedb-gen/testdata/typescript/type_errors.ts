import { full } from "@spicedb/client";
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
