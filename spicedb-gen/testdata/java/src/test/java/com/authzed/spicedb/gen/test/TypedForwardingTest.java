package com.authzed.spicedb.gen.test;
import static org.junit.jupiter.api.Assertions.*;
import static com.authzed.spicedb.gen.test.Permissions.*;
import com.authzed.spicedb.*;
import com.authzed.api.v1.*;
import io.grpc.stub.StreamObserver;
import org.junit.jupiter.api.Test;
import java.time.Duration;
import java.util.*;
class TypedForwardingTest {
 @Test void checkOptionsAndNativeTraceSurviveGeneratedFacade() throws Exception {
  var requests=new ArrayList<CheckPermissionRequest>();
  var lookups=new ArrayList<LookupSubjectsRequest>();
  var server=io.grpc.ServerBuilder.forPort(0).addService(new PermissionsServiceGrpc.PermissionsServiceImplBase(){
   public void writeRelationships(WriteRelationshipsRequest request,StreamObserver<WriteRelationshipsResponse> out){out.onNext(WriteRelationshipsResponse.newBuilder().setWrittenAt(com.authzed.api.v1.ZedToken.newBuilder().setToken("write-revision")).build());out.onCompleted();}
   public void lookupResources(LookupResourcesRequest request,StreamObserver<LookupResourcesResponse> out){
    out.onNext(LookupResourcesResponse.newBuilder().setResourceObjectId("conditional").setPermissionship(LookupPermissionship.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION).build());
    out.onNext(LookupResourcesResponse.newBuilder().setResourceObjectId("unknown").setPermissionshipValue(99).build());
    out.onNext(LookupResourcesResponse.newBuilder().setResourceObjectId("granted").setPermissionship(LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION).build());out.onCompleted();
   }
   public void lookupSubjects(LookupSubjectsRequest request,StreamObserver<LookupSubjectsResponse> out){lookups.add(request);assertNotNull(io.grpc.Context.current().getDeadline());out.onCompleted();}
   public void checkPermission(CheckPermissionRequest request,StreamObserver<CheckPermissionResponse> out){requests.add(request);assertNotNull(io.grpc.Context.current().getDeadline());var trace=com.authzed.api.v1.DebugInformation.newBuilder().setSchemaUsed("generated trace").setCheck(CheckDebugTrace.newBuilder().setResource(request.getResource()).setPermission(request.getPermission()).setSubject(request.getSubject()).setWasCachedResult(false));out.onNext(CheckPermissionResponse.newBuilder().setPermissionship(CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION).setDebugTrace(trace).build());out.onCompleted();}
  }).build().start();
  try(var raw=SpiceDBClient.createPlaintext("localhost:"+server.getPort(),"test");var client=new TypedClient(raw)){
   try(var stream=client.lookupResources(com.authzed.spicedb.Consistency.full(),Document_View,User("alice"),LookupOptions.builder().permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY).limit(1).build())){assertEquals(List.of("granted"),stream.map(LookupResult.LookupResource::resourceId).toList());}
   var relationships = List.<TypedRelationship>of(Document("one").viewer(User("alice")));
   WriteResult touched = client.touch(relationships); assertEquals(new com.authzed.spicedb.ZedToken("write-revision"), touched.revision());
   WriteResult created = client.create(relationships); assertEquals(touched, created);
   WriteResult deleted = client.delete(relationships, WriteOptions.builder().build()); assertEquals(touched, deleted);
   assertNotNull(com.authzed.spicedb.Consistency.atLeast(touched.revision()));
   var result=client.check(com.authzed.spicedb.Consistency.full(),Document("one").view(),User("alice").withIpRange(new IpRangeContext().withAllowedCidr("item")),CheckOptions.builder().context(Map.of("allowed_cidr","default","region","us")).tracing(com.authzed.spicedb.CheckTracing.ENABLED).timeout(Duration.ofSeconds(2)).build());
   assertTrue(result.hasPermission());assertNull(result.checkedAt());assertEquals("generated trace",result.debugTrace().schemaUsed());assertEquals(new com.authzed.spicedb.DebugInformation.Cached(false),result.debugTrace().check().resolution());assertTrue(requests.get(0).getWithTracing());assertEquals("item",requests.get(0).getContext().getFieldsOrThrow("allowed_cidr").getStringValue());assertEquals("us",requests.get(0).getContext().getFieldsOrThrow("region").getStringValue());
   try(var stream=client.lookupSubjects(com.authzed.spicedb.Consistency.full(),Document("one").view(),TeamMemberType,LookupOptions.builder().timeout(Duration.ofSeconds(2)).context(Map.of("region","us")).build())){assertEquals(0,stream.count());}
   assertEquals("member",lookups.get(0).getOptionalSubjectRelation());assertEquals("team",lookups.get(0).getSubjectObjectType());assertEquals("us",lookups.get(0).getContext().getFieldsOrThrow("region").getStringValue());
   assertThrows(com.authzed.spicedb.errors.InvalidArgumentException.class,()->client.lookupSubjects(com.authzed.spicedb.Consistency.full(),Document("one").view(),TeamMemberType,LookupOptions.builder().subjectRelation("other").build()));
   client.close();assertTrue(client.check(com.authzed.spicedb.Consistency.full(),Document("one").view(),User("alice")).hasPermission());
  }
  try(var owned=TypedClient.createPlaintext("localhost:"+server.getPort(),"test",ClientOptions.builder().defaultTimeout(Duration.ofSeconds(2)).build())){
   assertTrue(owned.check(com.authzed.spicedb.Consistency.full(),Document("one").view(),User("alice")).hasPermission());
   owned.close();assertThrows(java.util.concurrent.CancellationException.class,()->owned.check(com.authzed.spicedb.Consistency.full(),Document("one").view(),User("alice")));
  }finally{server.shutdownNow();server.awaitTermination();}
 }
 @Test void remoteCredentialPolicyReachesTypedFactory() {
  assertThrows(com.authzed.spicedb.errors.InvalidArgumentException.class, () -> TypedClient.createPlaintext("remote.example:50051", "key"));
  for (String endpoint : List.of("remote.example:50051", "192.0.2.1:50051")) {
   try (var client = TypedClient.createPlaintext(endpoint, "key", ClientOptions.builder().remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW).build())) { assertNotNull(client.client()); }
  }
 }
}
