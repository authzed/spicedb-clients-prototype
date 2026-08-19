// Client
export { SpiceDBProtoClient, createSpiceDBClient, type ClientOptions } from "./client.js";

// Service definitions
export { PermissionsService } from "./gen/authzed/api/v1/permission_service_pb.js";
export { SchemaService } from "./gen/authzed/api/v1/schema_service_pb.js";
export { WatchService } from "./gen/authzed/api/v1/watch_service_pb.js";
export { ExperimentalService } from "./gen/authzed/api/v1/experimental_service_pb.js";

// Error detail types. `google.rpc.ErrorInfo` is how SpiceDB attaches a
// structured `ErrorReason` -- and the metadata behind it -- to a failed RPC's
// status; both are needed to decode an error detail instead of parsing an
// error message.
export {
  ErrorInfoSchema,
  type ErrorInfo,
} from "./gen/google/rpc/error_details_pb.js";
export {
  ErrorReason,
  ErrorReasonSchema,
} from "./gen/authzed/api/v1/error_reason_pb.js";

// Core proto types
export * from "./gen/authzed/api/v1/core_pb.js";
export * from "./gen/authzed/api/v1/permission_service_pb.js";
export * from "./gen/authzed/api/v1/schema_service_pb.js";
export * from "./gen/authzed/api/v1/watch_service_pb.js";
export * from "./gen/authzed/api/v1/experimental_service_pb.js";
