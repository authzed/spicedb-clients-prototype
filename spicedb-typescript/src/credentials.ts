import { X509Certificate, createPrivateKey, type KeyObject } from "node:crypto";
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

/** Parses every certificate; returns each piece's leaf (its first block), the one a client presents. */
function requireCertificates(name: string, value: Material): X509Certificate[] {
    const leaves: X509Certificate[] = [];
    for (const piece of pieces(value)) {
        const pem = text(piece);
        const blocks = pem?.match(PEM_CERTIFICATE) ?? [];
        if (blocks.length === 0)
            throw new InvalidArgumentError(`spicedb: ${name} is not a PEM-encoded X.509 certificate (no PEM CERTIFICATE block)`);
        blocks.forEach((block, i) => {
            let certificate: X509Certificate;
            try { certificate = new X509Certificate(block); }
            catch (error) { throw new InvalidArgumentError(`spicedb: ${name} is not a PEM-encoded X.509 certificate (${(error as Error).message})`, { cause: error }); }
            if (i === 0) leaves.push(certificate);
        });
    }
    return leaves;
}

function requirePrivateKeys(name: string, value: Material): KeyObject[] {
    return pieces(value).map(piece => {
        try {
            if (piece !== null && typeof piece === "object" && !(piece instanceof Uint8Array)) {
                const { pem, passphrase } = piece as { pem?: unknown; passphrase?: string };
                return createPrivateKey({ key: pem as string | Buffer, passphrase });
            }
            return createPrivateKey(piece as string | Buffer);
        }
        catch (error) { throw new InvalidArgumentError(`spicedb: ${name} is not a PEM-encoded private key (${(error as Error).message})`, { cause: error }); }
    });
}

/**
 * Parses each supplied TLS input, throwing `InvalidArgumentError` naming the
 * first that does not parse, or naming both client inputs when a client
 * certificate has no matching private key.
 */
export function requireParseableTls(tls: TlsOptions | undefined): void {
    if (tls?.caCert !== undefined) requireCertificates("tls.caCert", tls.caCert as Material);
    const certificates = tls?.clientCert === undefined ? [] : requireCertificates("tls.clientCert", tls.clientCert as Material);
    const keys = tls?.clientKey === undefined ? [] : requirePrivateKeys("tls.clientKey", tls.clientKey as Material);
    if (certificates.length === 0 || keys.length === 0)
        return;
    // Node pairs each certificate with the key of the same algorithm, so each
    // presented certificate needs some supplied key that matches it.
    for (const certificate of certificates) {
        if (!keys.some(key => { try { return certificate.checkPrivateKey(key); } catch { return false; } }))
            throw new InvalidArgumentError(`spicedb: tls.clientKey does not match tls.clientCert (the private key is not the one for certificate ${certificate.subject.replace(/\n/g, ", ")})`);
    }
}
