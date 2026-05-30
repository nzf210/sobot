import path from "path";

/**
 * Resolves `userPath` relative to `baseDir`, then verifies the resolved
 * absolute path stays within `baseDir`. Throws on path traversal attempts.
 */
export function sanitizePath(userPath: string, baseDir: string): string {
  const resolved = path.resolve(baseDir, userPath);
  const normalizedBase = path.resolve(baseDir) + path.sep;
  if (!resolved.startsWith(normalizedBase) && resolved !== path.resolve(baseDir)) {
    throw new Error(
      `Path traversal blocked: "${userPath}" resolves outside "${baseDir}"`
    );
  }
  return resolved;
}
