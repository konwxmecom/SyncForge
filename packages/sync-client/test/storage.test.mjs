import "fake-indexeddb/auto";
import assert from "node:assert/strict";
import test from "node:test";
import { createDocument } from "../../core/dist/index.js";
import { IndexedDBSyncStorage } from "../dist/index.js";

let databaseSequence = 0;

function createStorage() {
  databaseSequence += 1;
  return new IndexedDBSyncStorage(`syncforge-test-${databaseSequence}`);
}

test("persists document snapshots and pending operations across storage instances", async () => {
  databaseSequence += 1;
  const databaseName = `syncforge-test-${databaseSequence}`;
  const storage = new IndexedDBSyncStorage(databaseName);
  const document = createDocument("replica-a");
  const operations = document.insertAt(0, "offline");
  const queued = operations.map((operation, index) => ({
    operationId: `insert:replica-a:replica-a:${index + 1}`,
    operation
  }));

  await storage.saveLocalOperations("document-a", document.snapshot(), queued);

  const restored = await new IndexedDBSyncStorage(databaseName).load("document-a");
  assert.deepEqual(restored?.snapshot, document.snapshot());
  assert.deepEqual(restored?.pendingOperations, queued);
});

test("acknowledges an individual queued operation without removing other local work", async () => {
  const storage = createStorage();
  const document = createDocument("replica-a");
  const operations = document.insertAt(0, "xy");
  const queued = operations.map((operation, index) => ({
    operationId: `insert:replica-a:replica-a:${index + 1}`,
    operation
  }));
  await storage.saveLocalOperations("document-b", document.snapshot(), queued);

  await storage.acknowledge("document-b", queued[0].operationId);

  const state = await storage.load("document-b");
  assert.deepEqual(state?.pendingOperations, [queued[1]]);
});
