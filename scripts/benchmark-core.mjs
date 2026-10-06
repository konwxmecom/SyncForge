import os from "node:os";
import { performance } from "node:perf_hooks";
import { createDocument } from "../packages/core/dist/index.js";

const documentSize = 1_000;
const editCount = 250;
const initialText = "a".repeat(documentSize);
const writer = createDocument("benchmark-writer");
const replica = createDocument("benchmark-replica");

const seedStart = performance.now();
const seedOperations = writer.insertAt(0, initialText);
for (const operation of seedOperations) {
  replica.applyOperation(operation);
}
const seedMilliseconds = performance.now() - seedStart;

let operationBytes = 0;
const editStart = performance.now();
for (let index = 0; index < editCount; index += 1) {
  const position = (index * 37) % (documentSize + index + 1);
  const [operation] = writer.insertAt(position, String.fromCharCode(97 + (index % 26)));
  const message = JSON.stringify({
    protocolVersion: 1,
    type: "operation",
    documentId: "benchmark-doc",
    replicaId: writer.replicaId,
    operationId: `insert:${writer.replicaId}:${operation.id.replicaId}:${operation.id.counter}`,
    operation
  });
  operationBytes += Buffer.byteLength(message);
  replica.applyOperation(operation);
}
const editMilliseconds = performance.now() - editStart;

if (writer.render() !== replica.render()) {
  throw new Error("benchmark replicas did not converge");
}

console.log(JSON.stringify({
  benchmark: "single-process CRDT bootstrap, replication, and edits",
  environment: {
    node: process.version,
    v8: process.versions.v8,
    platform: `${os.platform()} ${os.release()}`,
    architecture: os.arch(),
    logicalCPUs: os.cpus().length,
    totalMemoryBytes: os.totalmem()
  },
  workload: {
    initialDocumentCharacters: documentSize,
    replicatedInitialOperations: seedOperations.length,
    singleCharacterEdits: editCount,
    finalDocumentCharacters: writer.render().length,
    serializedEditBytes: operationBytes
  },
  results: {
    initialSeedAndReplicationMilliseconds: Number(seedMilliseconds.toFixed(2)),
    editAndReplicationMilliseconds: Number(editMilliseconds.toFixed(2)),
    editOperationsPerSecond: Number((editCount / (editMilliseconds / 1_000)).toFixed(2)),
    replicasConverged: true
  }
}, null, 2));
