import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, lstat, rm, symlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const backupScript = new URL("../backup-data.sh", import.meta.url);

test("creates a private archive and refuses unsafe destinations", async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "syncforge-backup-test-"));
  t.after(async () => {
    await rm(root, { recursive: true, force: true });
  });

  const dataDir = path.join(root, "data");
  const backupDir = path.join(root, "backups");
  await mkdir(dataDir, { mode: 0o700 });
  await writeFile(path.join(dataDir, "doc.jsonl"), '{"sequence":1}\n', { mode: 0o600 });
  await mkdir(backupDir);
  const archive = path.join(backupDir, "data.tar.gz");

  const result = spawnSync("sh", [backupScript.pathname, dataDir, archive], { encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  assert.equal((await lstat(archive)).mode & 0o777, 0o600);

  const listing = spawnSync("tar", ["-tzf", archive], { encoding: "utf8" });
  assert.equal(listing.status, 0, listing.stderr);
  assert.match(listing.stdout, /doc\.jsonl/);

  const nested = spawnSync("sh", [backupScript.pathname, dataDir, path.join(dataDir, "backup.tar.gz")], {
    encoding: "utf8"
  });
  assert.notEqual(nested.status, 0);
  assert.match(nested.stderr, /outside the data directory/);

  const directoryTarget = path.join(backupDir, "directory");
  await mkdir(directoryTarget);
  const directoryResult = spawnSync("sh", [backupScript.pathname, dataDir, directoryTarget], { encoding: "utf8" });
  assert.notEqual(directoryResult.status, 0);
  assert.match(directoryResult.stderr, /must not be a directory/);

  const linkPath = path.join(backupDir, "linked.tar.gz");
  await symlink(archive, linkPath);
  const linked = spawnSync("sh", [backupScript.pathname, dataDir, linkPath], { encoding: "utf8" });
  assert.notEqual(linked.status, 0);
  assert.match(linked.stderr, /symbolic link/);
});

test("rejects a missing data directory", async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "syncforge-backup-missing-"));
  t.after(async () => {
    await rm(root, { recursive: true, force: true });
  });
  const result = spawnSync("sh", [backupScript.pathname, path.join(root, "missing"), path.join(root, "out.tar.gz")], {
    encoding: "utf8"
  });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /Data directory does not exist/);
});
