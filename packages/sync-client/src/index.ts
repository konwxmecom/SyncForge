import type { SingleReplicaDocument, TextOperation } from "@syncforge/core";
import type {
  AcknowledgementMessage,
  ErrorMessage,
  JoinMessage,
  OperationMessage,
  PresenceLeaveMessage,
  PresenceMessage,
  SyncMessage
} from "@syncforge/protocol-ts";
import { parseSyncMessage } from "@syncforge/protocol-ts";
import type { QueuedOperation, SyncStorage } from "./storage.js";

export { IndexedDBSyncStorage } from "./storage.js";
export type { PersistedSyncState, QueuedOperation, SyncStorage } from "./storage.js";

export interface WebSocketLike {
  readonly readyState: number;
  onopen: ((event: Event) => void) | null;
  onmessage: ((event: MessageEvent<string>) => void) | null;
  onerror: ((event: Event) => void) | null;
  onclose: ((event: CloseEvent) => void) | null;
  send(data: string): void;
  close(): void;
}

export type SocketFactory = (url: string) => WebSocketLike;
export type SyncStatus = "connecting" | "connected" | "reconnecting" | "offline" | "closed";
export type TextPresence = Pick<PresenceMessage, "anchor" | "head">;

export interface SyncClientOptions {
  url: string;
  documentId: string;
  replicaId: string;
  authToken?: string;
  document: SingleReplicaDocument;
  storage?: SyncStorage;
  reconnect?: false | {
    initialDelayMs?: number;
    maxDelayMs?: number;
  };
  createSocket?: SocketFactory;
  onChange?: (text: string, message: OperationMessage) => void;
  onAcknowledgement?: (message: AcknowledgementMessage) => void;
  onPresence?: (message: PresenceMessage) => void;
  onPresenceLeave?: (message: PresenceLeaveMessage) => void;
  onError?: (message: ErrorMessage | Error) => void;
  onStatus?: (status: SyncStatus) => void;
}

export class SyncClient {
  private readonly options: SyncClientOptions;
  private socket: WebSocketLike | undefined;
  private joined = false;
  private connectionPromise: Promise<void> | undefined;
  private initialized = false;
  private readonly initialSnapshot: string;
  private pendingOperations: QueuedOperation[] = [];
  private readonly sentOnConnection = new Set<string>();
  private reconnectEnabled = false;
  private reconnectAttempt = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  private presenceTimer: ReturnType<typeof setTimeout> | undefined;
  private localPresence: TextPresence | undefined;

  constructor(options: SyncClientOptions) {
    if (!options.url || !options.documentId || !options.replicaId) {
      throw new TypeError("url, documentId, and replicaId are required");
    }
    if (options.document.replicaId !== options.replicaId) {
      throw new TypeError("document replicaId must match the sync client replicaId");
    }
    if (options.reconnect !== undefined && options.reconnect !== false) {
      const initial = options.reconnect.initialDelayMs ?? 250;
      const maximum = options.reconnect.maxDelayMs ?? 10_000;
      if (!Number.isFinite(initial) || !Number.isFinite(maximum) || initial < 1 || maximum < initial) {
        throw new TypeError("reconnect delays must be finite, positive, and maxDelayMs must be at least initialDelayMs");
      }
    }
    this.options = options;
    this.initialSnapshot = JSON.stringify(options.document.snapshot());
  }

  connect(): Promise<void> {
    this.reconnectEnabled = this.options.reconnect !== false;
    if (this.joined) {
      return Promise.resolve();
    }
    if (this.connectionPromise) {
      return this.connectionPromise;
    }

    this.options.onStatus?.(this.reconnectAttempt > 0 ? "reconnecting" : "connecting");
    this.connectionPromise = this.openConnection();
    const attempt = this.connectionPromise;
    void attempt.catch(() => {
      if (this.connectionPromise === attempt) {
        this.connectionPromise = undefined;
      }
      this.scheduleReconnect();
    });
    return this.connectionPromise;
  }

  async initialize(): Promise<void> {
    if (this.initialized) {
      return;
    }
    const saved = await this.options.storage?.load(this.options.documentId);
    if (saved?.snapshot) {
      if (saved.snapshot.replicaId !== this.options.replicaId) {
        throw new Error("Persisted document belongs to a different replicaId");
      }
      if (JSON.stringify(this.options.document.snapshot()) !== this.initialSnapshot) {
        throw new Error("Initialize the sync client before editing to avoid replacing unsaved local state");
      }
      this.options.document.restore(saved.snapshot);
    }
    this.pendingOperations = (saved?.pendingOperations ?? []).map((entry) => {
      const expectedId = operationIdFor(this.options.replicaId, entry.operation);
      if (entry.operationId !== expectedId) {
        throw new Error("Persisted operation queue contains an invalid operation ID");
      }
      parseSyncMessage(JSON.stringify({
        protocolVersion: 1,
        type: "operation",
        documentId: this.options.documentId,
        replicaId: this.options.replicaId,
        operationId: entry.operationId,
        operation: entry.operation
      }));
      return {
        operationId: entry.operationId,
        operation: entry.operation
      };
    });
    this.initialized = true;
  }

  get isConnected(): boolean {
    return this.joined && this.socket?.readyState === 1;
  }

  async sendOperations(operations: readonly TextOperation[]): Promise<void> {
    if (!this.initialized) {
      if (this.options.storage) {
        throw new Error("Call initialize() before editing when persistent storage is enabled");
      }
      this.initialized = true;
    }
    const queued = operations.map((operation) => ({
      operationId: operationIdFor(this.options.replicaId, operation),
      operation
    }));
    const known = new Set(this.pendingOperations.map((entry) => entry.operationId));
    const newOperations = queued.filter((entry) => !known.has(entry.operationId));
    if (this.options.storage && newOperations.length > 0) {
      await this.options.storage.saveLocalOperations(
        this.options.documentId,
        this.options.document.snapshot(),
        newOperations
      );
    }
    this.pendingOperations.push(...newOperations);
    await this.flushPending();
  }

  sendPresence(anchor: number, head: number): void {
    if (!Number.isSafeInteger(anchor) || !Number.isSafeInteger(head)
      || anchor < 0 || head < 0 || anchor > 10_000_000 || head > 10_000_000) {
      throw new TypeError("presence offsets must be safe integers between 0 and 10000000");
    }
    this.localPresence = { anchor, head };
    this.schedulePresence();
  }

  close(): void {
    this.reconnectEnabled = false;
    if (this.reconnectTimer !== undefined) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = undefined;
    }
    if (this.presenceTimer !== undefined) {
      clearTimeout(this.presenceTimer);
      this.presenceTimer = undefined;
    }
    this.socket?.close();
    this.options.onStatus?.("closed");
  }

  private async openConnection(): Promise<void> {
    await this.initialize();
    const createSocket = this.options.createSocket ?? ((url: string) => new WebSocket(url));
    const socket = createSocket(this.options.url);
    this.socket = socket;
    this.sentOnConnection.clear();

    await new Promise<void>((resolve, reject) => {
      let settled = false;
      socket.onopen = () => {
        const join: JoinMessage = {
          protocolVersion: 1,
          type: "join",
          documentId: this.options.documentId,
          replicaId: this.options.replicaId,
          ...(this.options.authToken === undefined ? {} : { authToken: this.options.authToken })
        };
        socket.send(JSON.stringify(join));
      };
      socket.onmessage = (event: MessageEvent<string>) => {
        void (async () => {
          try {
            const message = parseSyncMessage(event.data);
            await this.handleMessage(message);
            if (message.type === "joined" && !settled) {
              settled = true;
              resolve();
            }
          } catch (error) {
            const failure = error instanceof Error ? error : new Error(String(error));
            this.options.onError?.(failure);
            if (!settled) {
              settled = true;
              reject(failure);
            }
            socket.close();
          }
        })();
      };
      socket.onerror = () => {
        const failure = new Error("WebSocket connection failed");
        this.options.onError?.(failure);
        if (!settled) {
          settled = true;
          reject(failure);
        }
      };
      socket.onclose = () => {
        this.joined = false;
        this.connectionPromise = undefined;
        this.sentOnConnection.clear();
        if (!settled) {
          settled = true;
          reject(new Error("WebSocket closed before joining the document"));
        }
        if (this.reconnectEnabled) {
          this.options.onStatus?.("reconnecting");
        } else {
          this.options.onStatus?.("offline");
        }
        this.scheduleReconnect();
      };
    });
  }

  private async flushPending(): Promise<void> {
    const socket = this.socket;
    if (!this.joined || !socket || socket.readyState !== 1) {
      return;
    }

    for (const queued of this.pendingOperations) {
      if (this.sentOnConnection.has(queued.operationId)) {
        continue;
      }
      const message: OperationMessage = {
        protocolVersion: 1,
        type: "operation",
        documentId: this.options.documentId,
        replicaId: this.options.replicaId,
        operationId: queued.operationId,
        operation: queued.operation
      };
      socket.send(JSON.stringify(message));
      this.sentOnConnection.add(queued.operationId);
    }
  }

  private async handleMessage(message: SyncMessage): Promise<void> {
    if (message.type === "joined") {
      if (message.documentId !== this.options.documentId) {
        const failure = new Error("Server joined a different document");
        this.options.onError?.(failure);
        throw failure;
      }
      this.joined = true;
      this.reconnectAttempt = 0;
      this.options.onStatus?.("connected");
      this.sendPresenceNow();
      await this.flushPending();
      return;
    }

    if (message.type === "operation") {
      if (message.documentId !== this.options.documentId) {
        throw new Error("Received operation for a different document");
      }
      this.options.document.applyOperation(message.operation);
      await this.options.storage?.saveSnapshot(this.options.documentId, this.options.document.snapshot());
      this.options.onChange?.(this.options.document.render(), message);
      return;
    }

    if (message.type === "ack") {
      if (message.documentId !== this.options.documentId) {
        throw new Error("Received acknowledgement for a different document");
      }
      await this.options.storage?.acknowledge(this.options.documentId, message.operationId);
      this.pendingOperations = this.pendingOperations.filter((entry) => entry.operationId !== message.operationId);
      this.sentOnConnection.delete(message.operationId);
      this.options.onAcknowledgement?.(message);
      return;
    }

    if (message.type === "presence") {
      if (message.documentId !== this.options.documentId) {
        throw new Error("Received presence for a different document");
      }
      if (message.replicaId !== this.options.replicaId) {
        this.options.onPresence?.(message);
      }
      return;
    }

    if (message.type === "presence_leave") {
      if (message.documentId !== this.options.documentId) {
        throw new Error("Received presence leave for a different document");
      }
      if (message.replicaId !== this.options.replicaId) {
        this.options.onPresenceLeave?.(message);
      }
      return;
    }

    if (message.type === "error") {
      this.options.onError?.(message);
      if (!this.joined) {
        throw new Error(`${message.code}: ${message.message}`);
      }
      return;
    }

    throw new Error("Unexpected join message from sync server");
  }

  private scheduleReconnect(): void {
    if (!this.reconnectEnabled || this.reconnectTimer !== undefined || this.joined) {
      return;
    }
    const settings = this.options.reconnect === false ? undefined : this.options.reconnect;
    const initialDelayMs = settings?.initialDelayMs ?? 250;
    const maxDelayMs = settings?.maxDelayMs ?? 10_000;
    const delay = Math.min(initialDelayMs * (2 ** this.reconnectAttempt), maxDelayMs);
    this.reconnectAttempt += 1;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = undefined;
      if (this.reconnectEnabled) {
        void this.connect().catch((error: unknown) => {
          const failure = error instanceof Error ? error : new Error(String(error));
          this.options.onError?.(failure);
        });
      }
    }, delay);
  }

  private schedulePresence(): void {
    if (!this.joined || this.presenceTimer !== undefined) {
      return;
    }
    this.presenceTimer = setTimeout(() => {
      this.presenceTimer = undefined;
      this.sendPresenceNow();
    }, 50);
  }

  private sendPresenceNow(): void {
    const socket = this.socket;
    if (!this.joined || !this.localPresence || !socket || socket.readyState !== 1) {
      return;
    }
    if (this.presenceTimer !== undefined) {
      clearTimeout(this.presenceTimer);
      this.presenceTimer = undefined;
    }
    socket.send(JSON.stringify({
      protocolVersion: 1,
      type: "presence",
      documentId: this.options.documentId,
      replicaId: this.options.replicaId,
      ...this.localPresence
    } satisfies PresenceMessage));
  }
}

export function operationIdFor(replicaId: string, operation: TextOperation): string {
  const id = operation.id;
  return `${operation.kind}:${replicaId}:${id.replicaId}:${id.counter}`;
}
