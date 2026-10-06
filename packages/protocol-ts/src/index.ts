export interface ElementId {
  replicaId: string;
  counter: number;
}

export type SyncOperation =
  | { kind: "insert"; id: ElementId; after: ElementId | null; value: string }
  | { kind: "delete"; id: ElementId };

export interface JoinMessage {
  protocolVersion: 1;
  type: "join";
  documentId: string;
  replicaId: string;
  authToken?: string;
}

export interface OperationMessage {
  protocolVersion: 1;
  type: "operation";
  documentId: string;
  replicaId: string;
  operationId: string;
  operation: SyncOperation;
}

export interface AcknowledgementMessage {
  protocolVersion: 1;
  type: "ack";
  documentId: string;
  operationId: string;
}

export interface JoinedMessage {
  protocolVersion: 1;
  type: "joined";
  documentId: string;
}

export interface PresenceMessage {
  protocolVersion: 1;
  type: "presence";
  documentId: string;
  replicaId: string;
  anchor: number;
  head: number;
}

export interface PresenceLeaveMessage {
  protocolVersion: 1;
  type: "presence_leave";
  documentId: string;
  replicaId: string;
}

export interface ErrorMessage {
  protocolVersion: 1;
  type: "error";
  code: string;
  message: string;
}

export type SyncMessage =
  | JoinMessage
  | OperationMessage
  | AcknowledgementMessage
  | JoinedMessage
  | PresenceMessage
  | PresenceLeaveMessage
  | ErrorMessage;

const identifierPattern = /^[A-Za-z0-9._:-]{1,128}$/;
const maxPresenceOffset = 10_000_000;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasExactKeys(value: Record<string, unknown>, keys: string[]): boolean {
  const actualKeys = Object.keys(value).sort();
  const expectedKeys = [...keys].sort();
  return actualKeys.length === expectedKeys.length
    && actualKeys.every((key, index) => key === expectedKeys[index]);
}

function isIdentifier(value: unknown): value is string {
  return typeof value === "string" && identifierPattern.test(value);
}

function isOperationId(value: unknown): value is string {
  return typeof value === "string" && /^[A-Za-z0-9._:-]{1,512}$/.test(value);
}

function isElementId(value: unknown): value is { replicaId: string; counter: number } {
  return isRecord(value)
    && hasExactKeys(value, ["replicaId", "counter"])
    && isIdentifier(value.replicaId)
    && Number.isSafeInteger(value.counter)
    && (value.counter as number) > 0;
}

function isOperation(value: unknown): value is SyncOperation {
  if (!isRecord(value) || typeof value.kind !== "string") {
    return false;
  }

  if (value.kind === "insert") {
    return hasExactKeys(value, ["kind", "id", "after", "value"])
      && isElementId(value.id)
      && (value.after === null || isElementId(value.after))
      && typeof value.value === "string"
      && Array.from(value.value).length === 1
      && (value.after === null
        || value.after.replicaId !== value.id.replicaId
        || value.after.counter !== value.id.counter);
  }

  return value.kind === "delete"
    && hasExactKeys(value, ["kind", "id"])
    && isElementId(value.id);
}

export function parseSyncMessage(input: string): SyncMessage {
  const value: unknown = JSON.parse(input);
  if (!isRecord(value) || value.protocolVersion !== 1 || typeof value.type !== "string") {
    throw new TypeError("Sync message must be a protocol v1 JSON object");
  }

  switch (value.type) {
    case "join":
      if (!hasExactKeys(value, value.authToken === undefined
        ? ["protocolVersion", "type", "documentId", "replicaId"]
        : ["protocolVersion", "type", "documentId", "replicaId", "authToken"])
        || !isIdentifier(value.documentId)
        || !isIdentifier(value.replicaId)
        || (value.authToken !== undefined && (typeof value.authToken !== "string" || value.authToken.length === 0 || value.authToken.length > 512))) {
        throw new TypeError("Invalid join message");
      }
      return {
        protocolVersion: 1,
        type: "join",
        documentId: value.documentId,
        replicaId: value.replicaId,
        ...(value.authToken === undefined ? {} : { authToken: value.authToken })
      };
    case "operation":
      if (!hasExactKeys(value, ["protocolVersion", "type", "documentId", "replicaId", "operationId", "operation"])
        || !isIdentifier(value.documentId)
        || !isIdentifier(value.replicaId)
        || !isOperationId(value.operationId)
        || !isOperation(value.operation)
        || (value.operation.kind === "insert" && value.operation.id.replicaId !== value.replicaId)) {
        throw new TypeError("Invalid operation message");
      }
      return {
        protocolVersion: 1,
        type: "operation",
        documentId: value.documentId,
        replicaId: value.replicaId,
        operationId: value.operationId,
        operation: value.operation
      };
    case "ack":
      if (!hasExactKeys(value, ["protocolVersion", "type", "documentId", "operationId"])
        || !isIdentifier(value.documentId)
        || !isOperationId(value.operationId)) {
        throw new TypeError("Invalid acknowledgement message");
      }
      return {
        protocolVersion: 1,
        type: "ack",
        documentId: value.documentId,
        operationId: value.operationId
      };
    case "joined":
      if (!hasExactKeys(value, ["protocolVersion", "type", "documentId"])
        || !isIdentifier(value.documentId)) {
        throw new TypeError("Invalid joined message");
      }
      return {
        protocolVersion: 1,
        type: "joined",
        documentId: value.documentId
      };
    case "presence":
      if (!hasExactKeys(value, ["protocolVersion", "type", "documentId", "replicaId", "anchor", "head"])
        || !isIdentifier(value.documentId)
        || !isIdentifier(value.replicaId)
        || !isPresenceOffset(value.anchor)
        || !isPresenceOffset(value.head)) {
        throw new TypeError("Invalid presence message");
      }
      return {
        protocolVersion: 1,
        type: "presence",
        documentId: value.documentId,
        replicaId: value.replicaId,
        anchor: value.anchor,
        head: value.head
      };
    case "presence_leave":
      if (!hasExactKeys(value, ["protocolVersion", "type", "documentId", "replicaId"])
        || !isIdentifier(value.documentId)
        || !isIdentifier(value.replicaId)) {
        throw new TypeError("Invalid presence leave message");
      }
      return {
        protocolVersion: 1,
        type: "presence_leave",
        documentId: value.documentId,
        replicaId: value.replicaId
      };
    case "error":
      if (!hasExactKeys(value, ["protocolVersion", "type", "code", "message"])
        || typeof value.code !== "string"
        || value.code.length === 0
        || typeof value.message !== "string"
        || value.message.length === 0) {
        throw new TypeError("Invalid error message");
      }

      function isPresenceOffset(value: unknown): value is number {
        return Number.isSafeInteger(value) && (value as number) >= 0 && (value as number) <= maxPresenceOffset;
      }
      return {
        protocolVersion: 1,
        type: "error",
        code: value.code,
        message: value.message
      };
    default:
      throw new TypeError(`Unsupported sync message type: ${value.type}`);
  }
}
