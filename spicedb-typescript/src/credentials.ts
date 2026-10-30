import { X509Certificate, createPrivateKey } from "node:crypto";
import type { TlsOptions } from "@spicedb/proto";
import { InvalidArgumentError } from "./errors.js";

// Root DESIGN.md, "Reject unusable credentials and trust material at
// construction": a blank token and trust material that does not parse fail
// when the client is built, naming the input, never as a bare "Bearer"
// header or an unexplained handshake failure on the first call.

/** Throws `InvalidArgumentError` for an empty or whitespace-only token. */
export function requireUsableToken(token: unknown): void {
    if (typeof token !== "string" || token.trim() === "")
        throw new InvalidArgumentError("spicedb: token must be a nonempty string, not empty or whitespace (check that the variable holding it is set)");
}

const PEM_CERTIFICATE = /-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/g;

type Material = string | Buffer | Uint8Array | { pem?: unknown; passphrase?: string } | readonly unknown[];

function pieces(value: unknown): unknown[] {
    return Array.isArray(value) ? value : [value];
}

function text(piece: unknown): string | undefined {
    if (typeof piece === "string") return piece;
    if (piece instanceof Uint8Array) return Buffer.from(piece).toString("latin1");
    return undefined;
}

function requireCertificates(name: string, value: Material): void {
    for (const piece of pieces(value)) {
        const pem = text(piece);
        const blocks = pem?.match(PEM_CERTIFICATE) ?? [];
        if (blocks.length === 0)
            throw new InvalidArgumentError(`spicedb: ${name} is not a PEM-encoded X.509 certificate (no PEM CERTIFICATE block)`);
        for (const block of blocks) {
            try { new X509Certificate(block); }
            catch (error) { throw new InvalidArgumentError(`spicedb: ${name} is not a PEM-encoded X.509 certificate (${(error as Error).message})`, { cause: error }); }
        }
    }
}

function requirePrivateKeys(name: string, value: Material): void {
    for (const piece of pieces(value)) {
        try {
            if (piece !== null && typeof piece === "object" && !(piece instanceof Uint8Array)) {
                const { pem, passphrase } = piece as { pem?: unknown; passphrase?: string };
                createPrivateKey({ key: pem as string | Buffer, passphrase });
            } else {
                createPrivateKey(piece as string | Buffer);
            }
        }
        catch (error) { throw new InvalidArgumentError(`spicedb: ${name} is not a PEM-encoded private key (${(error as Error).message})`, { cause: error }); }
    }
}

/** Parses each supplied TLS input, throwing `InvalidArgumentError` naming the first that does not parse. */
export function requireParseableTls(tls: TlsOptions | undefined): void {
    if (tls?.caCert !== undefined) requireCertificates("tls.caCert", tls.caCert as Material);
    if (tls?.clientCert !== undefined) requireCertificates("tls.clientCert", tls.clientCert as Material);
    if (tls?.clientKey !== undefined) requirePrivateKeys("tls.clientKey", tls.clientKey as Material);
}
