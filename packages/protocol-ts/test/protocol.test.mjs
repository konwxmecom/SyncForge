import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { parseSyncMessage } from "../dist/index.js";

const fixture = await readFile("protocol/examples/join.v1.json", "utf8");

test("parses the shared v1 join example", () => {
  assert.deepEqual(parseSyncMessage(fixture), {
    protocolVersion: 1,
    type: "join",
    documentId: "demo-doc",
    replicaId: "browser-tab-a"
  });
});

test("rejects unsupported versions and unknown fields", () => {
  assert.throws(() => parseSyncMessage(fixture.replace('"protocolVersion": 1', '"protocolVersion": 2')));
  assert.throws(() => parseSyncMessage(fixture.replace('"type": "join"', '"type": "unknown"')));
  assert.throws(() => parseSyncMessage(fixture.replace('"type": "join"', '"type": "join", "extra": true')));
});

test("rejects invalid identifiers", () => {
  assert.throws(() => parseSyncMessage(fixture.replace('"documentId": "demo-doc"', '"documentId": ""')));
});

test("parses an optional access token without allowing malformed credentials", () => {
  const join = JSON.parse(fixture);
  join.authToken = "01234567890123456789012345678901";
  assert.deepEqual(parseSyncMessage(JSON.stringify(join)), join);
  assert.throws(() => parseSyncMessage(JSON.stringify({ ...join, authToken: "" })));
  assert.throws(() => parseSyncMessage(JSON.stringify({ ...join, unexpected: true })));
});

test("parses operation, acknowledgement, joined, and error messages", () => {
  const operation = {
    protocolVersion: 1,
    type: "operation",
    documentId: "demo-doc",
    replicaId: "browser-tab-a",
    operationId: "insert:browser-tab-a:browser-tab-a:1",
    operation: {
      kind: "insert",
      id: { replicaId: "browser-tab-a", counter: 1 },
      after: null,
      value: "x"
    }
  };
  assert.deepEqual(parseSyncMessage(JSON.stringify(operation)), operation);
  assert.deepEqual(parseSyncMessage(JSON.stringify({
    protocolVersion: 1,
    type: "ack",
    documentId: "demo-doc",
    operationId: operation.operationId
  })), {
    protocolVersion: 1,
    type: "ack",
    documentId: "demo-doc",
    operationId: operation.operationId
  });
  assert.deepEqual(parseSyncMessage('{"protocolVersion":1,"type":"joined","documentId":"demo-doc"}'), {
    protocolVersion: 1,
    type: "joined",
    documentId: "demo-doc"
  });
  assert.deepEqual(parseSyncMessage('{"protocolVersion":1,"type":"error","code":"bad_message","message":"Invalid message"}'), {
    protocolVersion: 1,
    type: "error",
    code: "bad_message",
    message: "Invalid message"
  });
});

test("parses bounded ephemeral presence and leave messages", () => {
  const presence = {
    protocolVersion: 1,
    type: "presence",
    documentId: "demo-doc",
    replicaId: "browser-tab-a",
    anchor: 0,
    head: 12
  };
  assert.deepEqual(parseSyncMessage(JSON.stringify(presence)), presence);
  assert.deepEqual(parseSyncMessage(JSON.stringify({
    protocolVersion: 1,
    type: "presence_leave",
    documentId: "demo-doc",
    replicaId: "browser-tab-a"
  })), {
    protocolVersion: 1,
    type: "presence_leave",
    documentId: "demo-doc",
    replicaId: "browser-tab-a"
  });
  assert.throws(() => parseSyncMessage(JSON.stringify({ ...presence, head: -1 })));
  assert.throws(() => parseSyncMessage(JSON.stringify({ ...presence, anchor: 10_000_001 })));
  assert.throws(() => parseSyncMessage(JSON.stringify({ ...presence, extra: true })));
});

test("rejects malformed operation envelopes and operations", () => {
  const base = {
    protocolVersion: 1,
    type: "operation",
    documentId: "demo-doc",
    replicaId: "browser-tab-a",
    operationId: "insert:browser-tab-a:browser-tab-a:1",
    operation: {
      kind: "insert",
      id: { replicaId: "browser-tab-a", counter: 1 },
      after: null,
      value: "x"
    }
  };
  assert.throws(() => parseSyncMessage(JSON.stringify({
    ...base,
    operation: { ...base.operation, value: "two characters" }
  })));
  assert.throws(() => parseSyncMessage(JSON.stringify({
    ...base,
    operation: { ...base.operation, unexpected: true }
  })));
  assert.throws(() => parseSyncMessage(JSON.stringify({ ...base, replicaId: "not allowed!" })));
  assert.throws(() => parseSyncMessage(JSON.stringify({
    ...base,
    replicaId: "different-replica"
  })));
});
