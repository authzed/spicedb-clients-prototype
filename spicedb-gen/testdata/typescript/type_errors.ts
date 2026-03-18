import { full } from "@spicedb/client";
import { TypedClient, Document, User, Team } from "./permissions";

declare const tc: TypedClient;

// @ts-expect-error: edit only reachable by user, not team#member
tc.check(full(), Document("x").edit, Team.member("eng"));

// @ts-expect-error: delete only reachable by user
tc.check(full(), Document("x").delete, Team.member("eng"));

// @ts-expect-error: editor relation only allows user
Document("x").editor(Team("eng"));

// @ts-expect-error: owner relation only allows user
Document("x").owner(Team.member("eng"));

// @ts-expect-error: user has no properties beyond _type and _id
User("alice").viewer;
