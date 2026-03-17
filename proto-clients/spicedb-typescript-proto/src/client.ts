import { createClient, type Client, type Transport } from "@connectrpc/connect";
import { createGrpcTransport } from "@connectrpc/connect-node";
import { PermissionsService } from "./gen/authzed/api/v1/permission_service_pb.js";
import { SchemaService } from "./gen/authzed/api/v1/schema_service_pb.js";
import { WatchService } from "./gen/authzed/api/v1/watch_service_pb.js";
import { ExperimentalService } from "./gen/authzed/api/v1/experimental_service_pb.js";

/**
 * Optional configuration for the SpiceDB proto client.
 */
export interface ClientOptions {
  /** Use plaintext (insecure) connection instead of TLS. */
  insecure?: boolean;
  /** Additional headers to include in every request. */
  headers?: Record<string, string>;
}

/**
 * SpiceDBProtoClient wraps all generated Connect-ES service clients.
 */
export class SpiceDBProtoClient {
  readonly permissions: Client<typeof PermissionsService>;
  readonly schema: Client<typeof SchemaService>;
  readonly watch: Client<typeof WatchService>;
  readonly experimental: Client<typeof ExperimentalService>;

  constructor(transport: Transport) {
    this.permissions = createClient(PermissionsService, transport);
    this.schema = createClient(SchemaService, transport);
    this.watch = createClient(WatchService, transport);
    this.experimental = createClient(ExperimentalService, transport);
  }
}

/**
 * Creates a SpiceDBProtoClient connected to the given endpoint with bearer
 * token authentication.
 */
export function createSpiceDBClient(
  endpoint: string,
  token: string,
  options?: ClientOptions,
): SpiceDBProtoClient {
  const transport = createGrpcTransport({
    baseUrl: options?.insecure ? `http://${endpoint}` : `https://${endpoint}`,
    interceptors: [
      (next) => (req) => {
        req.header.set("authorization", `Bearer ${token}`);
        if (options?.headers) {
          for (const [key, value] of Object.entries(options.headers)) {
            req.header.set(key, value);
          }
        }
        return next(req);
      },
    ],
  });
  return new SpiceDBProtoClient(transport);
}
