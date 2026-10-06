import assert from "node:assert/strict";
import test from "node:test";
import "fake-indexeddb/auto";
import { createDocument } from "../../core/dist/index.js";
import { IndexedDBSyncStorage, operationIdFor, SyncClient } from "../dist/index.js";

class FakeSocket {
  readyState = 0;
  onopen = null;
  onmessage = null;
  onerror = null;
  onclose = null;
  sent = [];
  onSend = null;

  send(data) {
    this.sent.push(data);
    this.onSend?.(data);
  }

  close() {
    this.readyState = 3;
    this.onclose?.({ type: "close" });
  }

  open() {
    this.readyState = 1;
    this.onopen?.({ type: "open" });
  }

  receive(data) {
    this.onmessage?.({ data, type: "message" });
  }
}

class InMemoryRoomServer {
  sockets = new Set();
  operations = [];

  createSocket() {
    const socket = new FakeSocket();
    socket.onSend = (data) => this.handle(socket, JSON.parse(data));
    this.sockets.add(socket);
    return socket;
  }

  handle(sender, message) {
    if (message.type === "join") {
      for (const operation of this.operations) {
        sender.receive(JSON.stringify(operation));
      }
      sender.receive(JSON.stringify({
        protocolVersion: 1,
        type: "joined",
        documentId: message.documentId
      }));
      return;
    }

    if (message.type !== "operation") {
      return;
    }
    if (!this.operations.some((operation) => operation.operationId === message.operationId)) {
      this.operations.push(message);
      for (const socket of this.sockets) {
        if (socket !== sender && socket.readyState === 1) {
          socket.receive(JSON.stringify(message));
        }
      }
    }
    sender.receive(JSON.stringify({
      protocolVersion: 1,
      type: "ack",
      documentId: message.documentId,
      operationId: message.operationId
    }));
  }
}

test("joins a room, sends local operations, and applies remote operations", async () => {
  const socket = new FakeSocket();
  const document = createDocument("replica-a");
  const client = new SyncClient({
    url: "ws://localhost:8080/sync",
    documentId: "shared-doc",
    replicaId: "replica-a",
    document,
    createSocket: () => socket
  });
  const connecting = client.connect();
  await new Promise((resolve) => setImmediate(resolve));
  socket.open();

  assert.deepEqual(JSON.parse(socket.sent[0]), {
    protocolVersion: 1,
    type: "join",
    documentId: "shared-doc",
    replicaId: "replica-a"
  });
  socket.receive('{"protocolVersion":1,"type":"joined","documentId":"shared-doc"}');
  await connecting;

  const localOperations = document.insertAt(0, "hi");
  await client.sendOperations(localOperations);
  assert.equal(socket.sent.length, 3);
  assert.equal(JSON.parse(socket.sent[1]).operation.value, "h");
  assert.equal(JSON.parse(socket.sent[2]).operation.value, "i");
  assert.equal(JSON.parse(socket.sent[1]).operationId, "insert:replica-a:replica-a:1");

  socket.receive(JSON.stringify({
    protocolVersion: 1,
    type: "operation",
    documentId: "shared-doc",
    replicaId: "replica-b",
    operationId: "insert:replica-b:replica-b:1",
    operation: {
      kind: "insert",
      id: { replicaId: "replica-b", counter: 1 },
      after: null,
      value: "!"
    }
  }));
  assert.equal(document.render(), "hi!");
});

test("throttles local cursor presence, restores it after join, and handles peers leaving", async () => {
  const socket = new FakeSocket();
  const received = [];
  const departed = [];
  const client = new SyncClient({
    url: "ws://localhost:8080/sync",
    documentId: "presence-doc",
    replicaId: "replica-a",
    document: createDocument("replica-a"),
    createSocket: () => socket,
    onPresence: (message) => received.push(message),
    onPresenceLeave: (message) => departed.push(message)
  });
  client.sendPresence(0, 0);
  client.sendPresence(3, 5);
  const connecting = client.connect();
  await new Promise((resolve) => setImmediate(resolve));
  socket.open();
  socket.receive('{"protocolVersion":1,"type":"joined","documentId":"presence-doc"}');
  await connecting;

  const presence = socket.sent.map((message) => JSON.parse(message)).find((message) => message.type === "presence");
  assert.deepEqual(presence, {
    protocolVersion: 1,
    type: "presence",
    documentId: "presence-doc",
    replicaId: "replica-a",
    anchor: 3,
    head: 5
  });

  socket.receive(JSON.stringify({
    protocolVersion: 1,
    type: "presence",
    documentId: "presence-doc",
    replicaId: "replica-b",
    anchor: 1,
    head: 4
  }));
  socket.receive(JSON.stringify({
    protocolVersion: 1,
    type: "presence_leave",
    documentId: "presence-doc",
    replicaId: "replica-b"
  }));
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(received.length, 1);
  assert.equal(departed.length, 1);
  assert.throws(() => client.sendPresence(-1, 0), /presence offsets/);
  client.close();
});

test("restores an offline document and resends queued operations until acknowledged", async () => {
  const storage = new IndexedDBSyncStorage("sync-client-recovery-test");
  const offlineDocument = createDocument("replica-a");
  const offlineClient = new SyncClient({
    url: "ws://localhost:8080/sync",
    documentId: "recovery-doc",
    replicaId: "replica-a",
    document: offlineDocument,
    storage
  });
  await offlineClient.initialize();
  const localOperations = offlineDocument.insertAt(0, "saved");
  await offlineClient.sendOperations(localOperations);

  const socket = new FakeSocket();
  const restoredDocument = createDocument("replica-a");
  const restoredClient = new SyncClient({
    url: "ws://localhost:8080/sync",
    documentId: "recovery-doc",
    replicaId: "replica-a",
    document: restoredDocument,
    storage,
    createSocket: () => socket
  });
  await restoredClient.initialize();
  assert.equal(restoredDocument.render(), "saved");

  const connecting = restoredClient.connect();
  await new Promise((resolve) => setImmediate(resolve));
  socket.open();
  socket.receive('{"protocolVersion":1,"type":"joined","documentId":"recovery-doc"}');
  await connecting;
  const replayed = socket.sent.slice(1).map((message) => JSON.parse(message));
  assert.equal(replayed.length, localOperations.length);
  assert.equal(replayed[0].operationId, "insert:replica-a:replica-a:1");

  socket.receive(JSON.stringify({
    protocolVersion: 1,
    type: "ack",
    documentId: "recovery-doc",
    operationId: replayed[0].operationId
  }));
  await new Promise((resolve) => setImmediate(resolve));
  const persisted = await storage.load("recovery-doc");
  assert.equal(persisted.pendingOperations.length, localOperations.length - 1);
  restoredClient.close();
});

test("reconnects after an unexpected disconnect with a delayed retry", async () => {
  const sockets = [];
  const client = new SyncClient({
    url: "ws://localhost:8080/sync",
    documentId: "reconnect-doc",
    replicaId: "replica-a",
    document: createDocument("replica-a"),
    reconnect: { initialDelayMs: 5, maxDelayMs: 20 },
    createSocket: () => {
      const socket = new FakeSocket();
      sockets.push(socket);
      return socket;
    }
  });

  const connecting = client.connect();
  await new Promise((resolve) => setImmediate(resolve));
  sockets[0].open();
  sockets[0].receive('{"protocolVersion":1,"type":"joined","documentId":"reconnect-doc"}');
  await connecting;
  sockets[0].close();

  await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(sockets.length, 2);
  await new Promise((resolve) => setImmediate(resolve));
  sockets[1].open();
  sockets[1].receive('{"protocolVersion":1,"type":"joined","documentId":"reconnect-doc"}');
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(client.isConnected, true);
  client.close();
});

test("explicit close cancels a scheduled reconnect", async () => {
  const sockets = [];
  const client = new SyncClient({
    url: "ws://localhost:8080/sync",
    documentId: "close-reconnect-doc",
    replicaId: "replica-a",
    document: createDocument("replica-a"),
    reconnect: { initialDelayMs: 20, maxDelayMs: 20 },
    createSocket: () => {
      const socket = new FakeSocket();
      sockets.push(socket);
      return socket;
    }
  });

  const connecting = client.connect();
  await new Promise((resolve) => setImmediate(resolve));
  sockets[0].open();
  sockets[0].receive('{"protocolVersion":1,"type":"joined","documentId":"close-reconnect-doc"}');
  await connecting;
  sockets[0].close();
  client.close();
  await new Promise((resolve) => setTimeout(resolve, 30));
  assert.equal(sockets.length, 1);
});

test("offline replicas merge queued edits when reconnect replay restores missed operations", async () => {
  const server = new InMemoryRoomServer();
  const socketsA = [];
  const socketsB = [];
  const docA = createDocument("replica-a");
  const docB = createDocument("replica-b");
  const clientA = new SyncClient({
    url: "ws://localhost:8080/sync",
    documentId: "offline-merge-doc",
    replicaId: "replica-a",
    document: docA,
    reconnect: false,
    createSocket: () => {
      const socket = server.createSocket();
      socketsA.push(socket);
      return socket;
    }
  });
  const clientB = new SyncClient({
    url: "ws://localhost:8080/sync",
    documentId: "offline-merge-doc",
    replicaId: "replica-b",
    document: docB,
    reconnect: false,
    createSocket: () => {
      const socket = server.createSocket();
      socketsB.push(socket);
      return socket;
    }
  });

  const initialA = clientA.connect();
  const initialB = clientB.connect();
  await new Promise((resolve) => setImmediate(resolve));
  socketsA[0].open();
  socketsB[0].open();
  await Promise.all([initialA, initialB]);
  clientA.close();
  clientB.close();

  await clientA.sendOperations(docA.insertAt(0, "a"));
  await clientB.sendOperations(docB.insertAt(0, "b"));

  const reconnectA = clientA.connect();
  await new Promise((resolve) => setImmediate(resolve));
  socketsA[1].open();
  await reconnectA;

  const reconnectB = clientB.connect();
  await new Promise((resolve) => setImmediate(resolve));
  socketsB[1].open();
  await reconnectB;

  assert.equal(docA.render(), docB.render());
  assert.equal(docA.render(), "ab");
  clientA.close();
  clientB.close();
});

test("requires initialization before persistent local edits can be queued", async () => {
  const storage = new IndexedDBSyncStorage("sync-client-initialize-first-test");
  const document = createDocument("replica-a");
  const client = new SyncClient({
    url: "ws://localhost:8080/sync",
    documentId: "initialize-first-doc",
    replicaId: "replica-a",
    document,
    storage
  });
  const operations = document.insertAt(0, "x");

  await assert.rejects(
    client.sendOperations(operations),
    /initialize\(\) before editing/
  );
});

test("operation IDs distinguish an insert from deletion of the same element", () => {
  const id = { replicaId: "replica-a", counter: 1 };
  assert.notEqual(
    operationIdFor("replica-a", { kind: "insert", id, after: null, value: "x" }),
    operationIdFor("replica-a", { kind: "delete", id })
  );
});

test("rejects sending before the server confirms room membership", async () => {
  const socket = new FakeSocket();
  const client = new SyncClient({
    url: "ws://localhost:8080/sync",
    documentId: "shared-doc",
    replicaId: "replica-a",
    document: createDocument("replica-a"),
    createSocket: () => socket
  });
  const connecting = client.connect();
  await new Promise((resolve) => setImmediate(resolve));
  await client.sendOperations([]);
  assert.equal(socket.sent.length, 0);
  socket.close();
  await assert.rejects(connecting, /closed before joining/);
});
