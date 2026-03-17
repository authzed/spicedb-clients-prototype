// Client
export { SpiceDBProtoClient, createSpiceDBClient, type ClientOptions } from "./client.js";

// Service definitions
export { PermissionsService } from "./gen/authzed/api/v1/permission_service_pb.js";
export { SchemaService } from "./gen/authzed/api/v1/schema_service_pb.js";
export { WatchService } from "./gen/authzed/api/v1/watch_service_pb.js";
export { ExperimentalService } from "./gen/authzed/api/v1/experimental_service_pb.js";

// Core proto types
export * from "./gen/authzed/api/v1/core_pb.js";
export * from "./gen/authzed/api/v1/permission_service_pb.js";
export * from "./gen/authzed/api/v1/schema_service_pb.js";
export * from "./gen/authzed/api/v1/watch_service_pb.js";
export * from "./gen/authzed/api/v1/experimental_service_pb.js";
