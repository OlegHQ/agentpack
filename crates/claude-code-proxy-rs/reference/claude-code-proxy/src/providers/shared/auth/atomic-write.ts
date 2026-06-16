import { mkdir, writeFile, rename } from "node:fs/promises";
import { dirname } from "node:path";

export async function writeAtomicJson(path: string, data: unknown): Promise<void> {
  await mkdir(dirname(path), { recursive: true, mode: 0o700 });
  const tmp = `${path}.${process.pid}.${Date.now()}.tmp`;
  await writeFile(tmp, JSON.stringify(data, null, 2), { encoding: "utf8", mode: 0o600 });
  await rename(tmp, path);
}
