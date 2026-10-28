import "./token-equality.js";
import { TransportSecurity, SubjectRelationKind } from "../modes.js";
import {create} from "@bufbuild/protobuf";
import { describe, expect, it, vi } from "vitest";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { SpiceDBProtoClient, RelationshipsService, WatchPermissionSetsService, MaterializePermissionSetsProtocol as P } from "@spicedb/proto";
import { SpiceDBClient } from "../client.js";
import { ZedToken, type PermissionSetEvent } from "../types.js";
import { DeadlineExceededError, UnavailableError } from "../errors.js";

function client() {return new SpiceDBClient({endpoint:"localhost:1",token:"t",transportSecurity:TransportSecurity.Plaintext});}
const delay=(ms:number)=>new Promise<void>(resolve=>setTimeout(resolve,ms));
async function collect<T>(stream:AsyncIterableIterator<T>):Promise<T[]> {const result:T[]=[];for await(const value of stream)result.push(value);return result;}

describe("native materialize permission-set APIs",()=>{
 it("counts exact filtered tuples without inventing wire consistency",async()=>{
  let captured:unknown;
  const c=client();const raw=new SpiceDBProtoClient(createRouterTransport(router=>router.service(RelationshipsService,{
   experimentalCountRelationshipsByFilter(request){captured=request;return {relationshipCount:9007199254740993n,readAt:{token:"count"}};},
  })));
  (c as unknown as {proto:SpiceDBProtoClient}).proto=raw;
  const result=await c.experimentalCountRelationshipsByFilter({resourceType:"document",subject:{subjectType:"user",relation:{kind:SubjectRelationKind.Direct}}});
  expect(result).toEqual({stillCalculating:false,count:9007199254740993n,revision: new ZedToken("count")});
  expect(captured).toMatchObject({relationshipFilter:{optionalSubjectFilter:{optionalRelation:{relation:""}}}});expect(captured).not.toHaveProperty("consistency");c.close();
 });
 it("maps every watch event and every child/operation variant into native discriminated unions",async()=>{
  let start:string|undefined;const c=client();
  (c as unknown as {proto:SpiceDBProtoClient}).proto=new SpiceDBProtoClient(createRouterTransport(router=>router.service(WatchPermissionSetsService,{
   async *watchPermissionSets(request){
    start=request.optionalStartingAfter?.token;
    yield {response:{case:"change",value:{atRevision:{token:"r1"},operation:1,parentSet:{objectType:"document",objectId:"a",permissionOrRelation:"view"},child:{case:"childSet",value:{objectType:"group",objectId:"eng",permissionOrRelation:"member"}}}}};
    yield {response:{case:"change",value:{operation:2,child:{case:"childMember",value:{objectType:"user",objectId:"a",optionalPermissionOrRelation:"member"}}}}};
    yield {response:{case:"change",value:{operation:999 as never,child:{case:undefined}}}};
    yield {response:{case:"completedRevision",value:{token:"r2"}}};
    yield {response:{case:"lookupPermissionSetsRequired",value:{requiredLookupAt:{token:"r3"}}}};
    yield {response:{case:"breakingSchemaChange",value:{changeAt:{token:"r4"},affectedPermissions:[{resourceType:"document",permission:"view",subjectType:"group",optionalSubjectRelation:"member"}]}}};
    yield {response:{case:undefined}};
   },
  })));
  const events:PermissionSetEvent[]=await collect(c.experimentalWatchPermissionSets({startRevision:new ZedToken("start")}));expect(start).toBe("start");
  expect(events[0]).toEqual({kind:"change",change:{revision: new ZedToken("r1"),operation:"added",parentSet:{object:{objectType:"document",objectId:"a"},permissionOrRelation:"view"},child:{kind:"set",set:{object:{objectType:"group",objectId:"eng"},permissionOrRelation:"member"}}}});
  expect(events[1]).toMatchObject({kind:"change",change:{operation:"removed",child:{kind:"member",member:{object:{objectType:"user",objectId:"a"},permissionOrRelation:"member"}}}});
  expect(events[2]).toMatchObject({kind:"change",change:{operation:"unspecified",child:{kind:"unspecified"}}});
  expect(events.slice(3)).toEqual([{kind:"completedRevision",revision: new ZedToken("r2")},{kind:"lookupRequired",revision: new ZedToken("r3")},{kind:"breakingSchemaChange",revision: new ZedToken("r4"),affectedPermissions:[{resourceType:"document",permission:"view",subjectType:"group",subjectRelation:"member"}]},{kind:"unspecified"}]);expect(Object.isFrozen(events[0])).toBe(true);c.close();
 });
 it("automatically pages using every opaque cursor field",async()=>{
  const requests:P.LookupPermissionSetsRequest[]=[];const c=client();
  (c as unknown as {proto:SpiceDBProtoClient}).proto=new SpiceDBProtoClient(createRouterTransport(router=>router.service(WatchPermissionSetsService,{
   async *lookupPermissionSets(request){requests.push(request);const page=requests.length;yield {change:{atRevision:{token:"snapshot"},operation:1},cursor:{limit:2,token:{token:"snapshot"},startingIndex:page,completedMembers:page===2,startingKey:`key-${page}`,cursor:`opaque-${page}`}};},
  })));
  const values=await collect(c.experimentalLookupPermissionSets({revision:new ZedToken("requested"),pageSize:2}));expect(values).toHaveLength(2);expect(requests[0].optionalAtRevision?.token).toBe("requested");expect(requests[0].optionalStartingAfterCursor).toBeUndefined();
  expect(requests[1].optionalStartingAfterCursor).toMatchObject({limit:2,token:{token:"snapshot"},startingIndex:1,completedMembers:false,startingKey:"key-1",cursor:"opaque-1"});expect(values[1].cursor).toEqual({limit:2,token:new ZedToken("snapshot"),startingIndex:2,completedMembers:true,startingKey:"key-2",cursor:"opaque-2"});c.close();
 });
 it("bounds total results independently of page size and preserves a resume cursor",async()=>{
  const c=client();const rpc=vi.spyOn(c.raw().permissionSets,"lookupPermissionSets").mockImplementation(()=> (async function*(){yield {cursor:{limit:1,startingIndex:5,completedMembers:false,startingKey:"key",cursor:"opaque"}} as P.LookupPermissionSetsResponse;})());
  const values=await collect(c.experimentalLookupPermissionSets({pageSize:10,limit:1,cursor:{limit:1,startingIndex:4,completedMembers:false,startingKey:"prior",cursor:"before",token:new ZedToken("token")}}));expect(values).toHaveLength(1);expect(values[0].cursor?.cursor).toBe("opaque");expect(rpc).toHaveBeenCalledTimes(1);expect(rpc.mock.calls[0][0].limit).toBe(10);expect(rpc.mock.calls[0][0].optionalStartingAfterCursor?.token?.token).toBe("token");c.close();
 });
 it("shares one optional deadline over permission-set pages",async()=>{
  const c=client();let page=0;const rpc=vi.spyOn(c.raw().permissionSets,"lookupPermissionSets").mockImplementation(()=> (async function*(){await delay(30);yield create(P.LookupPermissionSetsResponseSchema,{cursor:{limit:1,startingIndex:++page,completedMembers:false,startingKey:"",cursor:`page-${page}`}});})());
  await expect(collect(c.experimentalLookupPermissionSets({pageSize:1},{timeoutMs:50}))).rejects.toBeInstanceOf(DeadlineExceededError);expect(rpc).toHaveBeenCalledTimes(2);expect(rpc.mock.calls[1][1]?.timeoutMs).toBeLessThan(30);c.close();
 });
 it("never reconnects permission-set watches automatically",async()=>{
  const c=client();const rpc=vi.spyOn(c.raw().permissionSets,"watchPermissionSets").mockImplementation(()=> (async function*(){throw new ConnectError("watch",Code.Unavailable);yield {} as never;})());await expect(collect(c.experimentalWatchPermissionSets())).rejects.toBeInstanceOf(UnavailableError);expect(rpc).toHaveBeenCalledTimes(1);c.close();
 });
 it("preserves snapshot file names, URLs, native timestamp and revision without fetching files",async()=>{
  const c=client();let revision:string|undefined;
  (c as unknown as {proto:SpiceDBProtoClient}).proto=new SpiceDBProtoClient(createRouterTransport(router=>router.service(WatchPermissionSetsService,{
   downloadPermissionSets(request){revision=request.optionalAtRevision?.token;return {files:[{name:"snapshot.avro",url:"https://download.invalid/snapshot"}],timestamp:{seconds:1767225600n,nanos:123000000},atRevision:{token:"snapshot"}};},
  })));
  const result=await c.experimentalDownloadPermissionSets({revision:new ZedToken("requested")});expect(revision).toBe("requested");expect(result.files).toEqual([{name:"snapshot.avro",url:"https://download.invalid/snapshot"}]);expect(result.timestamp?.toISOString()).toBe("2026-01-01T00:00:00.123Z");expect(result.revision.toString()).toBe("snapshot");expect(Object.isFrozen(result.files[0])).toBe(true);c.close();
 });
});
