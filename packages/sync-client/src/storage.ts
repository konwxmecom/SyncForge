import type { DocumentSnapshot, TextOperation } from "@syncforge/core";

export interface QueuedOperation {
  operationId: string;
  operation: TextOperation;
}

export interface PersistedSyncState {
  snapshot?: DocumentSnapshot;
  pendingOperations: QueuedOperation[];
}

export interface SyncStorage {
  load(documentId: string): Promise<PersistedSyncState | undefined>;
  saveSnapshot(documentId: string, snapshot: DocumentSnapshot): Promise<void>;
  saveLocalOperations(documentId: string, snapshot: DocumentSnapshot, operations: QueuedOperation[]): Promise<void>;
  acknowledge(documentId: string, operationId: string): Promise<void>;
}

interface StoredDocument {
  documentId: string;
  snapshot?: DocumentSnapshot;
  pendingOperations: QueuedOperation[];
}

const databaseVersion = 1;
const documentStore = "documents";

export class IndexedDBSyncStorage implements SyncStorage {
  private databasePromise: Promise<IDBDatabase> | undefined;

  constructor(
    private readonly databaseName = "syncforge",
    private readonly factory: IDBFactory = globalThis.indexedDB
  ) {
    if (!factory) {
      throw new Error("IndexedDB is not available in this environment");
    }
  }

  async load(documentId: string): Promise<PersistedSyncState | undefined> {
    const database = await this.openDatabase();
    const transaction = database.transaction(documentStore, "readonly");
    const result = await readTransaction<StoredDocument | undefined>(
      transaction,
      transaction.objectStore(documentStore).get(documentId)
    );
    if (!result) {
      return undefined;
    }
    if (!Array.isArray(result.pendingOperations)) {
      throw new TypeError(`Persisted operation queue for "${documentId}" is invalid`);
    }
    return {
      snapshot: result.snapshot,
      pendingOperations: result.pendingOperations
    };
  }

  async saveSnapshot(documentId: string, snapshot: DocumentSnapshot): Promise<void> {
    await this.update(documentId, (current) => ({ ...current, snapshot }));
  }

  async saveLocalOperations(
    documentId: string,
    snapshot: DocumentSnapshot,
    operations: QueuedOperation[]
  ): Promise<void> {
    await this.update(documentId, (current) => {
      const pending = new Map(current.pendingOperations.map((entry) => [entry.operationId, entry]));
      for (const entry of operations) {
        pending.set(entry.operationId, entry);
      }
      return {
        ...current,
        snapshot,
        pendingOperations: Array.from(pending.values())
      };
    });
  }

  async acknowledge(documentId: string, operationId: string): Promise<void> {
    await this.update(documentId, (current) => ({
      ...current,
      pendingOperations: current.pendingOperations.filter((entry) => entry.operationId !== operationId)
    }));
  }

  private async update(
    documentId: string,
    change: (state: PersistedSyncState) => PersistedSyncState
  ): Promise<void> {
    const database = await this.openDatabase();
    const transaction = database.transaction(documentStore, "readwrite");
    const store = transaction.objectStore(documentStore);
    const request = store.get(documentId);
    let updateFailure: unknown;

    request.onsuccess = () => {
      try {
        const current = request.result as StoredDocument | undefined;
        const changed = change({
          snapshot: current?.snapshot,
          pendingOperations: current?.pendingOperations ?? []
        });
        const value: StoredDocument = {
          documentId,
          snapshot: changed.snapshot,
          pendingOperations: changed.pendingOperations
        };
        store.put(value);
      } catch (error) {
        updateFailure = error;
        transaction.abort();
      }
    };
    try {
      await transactionComplete(transaction);
    } catch (error) {
      if (updateFailure !== undefined) {
        throw updateFailure;
      }
      throw error;
    }
  }

  private openDatabase(): Promise<IDBDatabase> {
    if (!this.databasePromise) {
      this.databasePromise = new Promise<IDBDatabase>((resolve, reject) => {
        const request = this.factory.open(this.databaseName, databaseVersion);
        request.onupgradeneeded = () => {
          if (!request.result.objectStoreNames.contains(documentStore)) {
            request.result.createObjectStore(documentStore, { keyPath: "documentId" });
          }
        };
        request.onsuccess = () => {
          const database = request.result;
          database.onversionchange = () => database.close();
          resolve(database);
        };
        request.onerror = () => reject(request.error ?? new Error("Unable to open IndexedDB"));
        request.onblocked = () => reject(new Error("IndexedDB upgrade is blocked by another connection"));
      }).catch((error: unknown) => {
        this.databasePromise = undefined;
        throw error;
      });
    }
    return this.databasePromise;
  }
}

function readTransaction<T>(transaction: IDBTransaction, request: IDBRequest<T>): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    let result: T;
    request.onsuccess = () => {
      result = request.result;
    };
    request.onerror = () => reject(request.error ?? new Error("IndexedDB request failed"));
    transaction.oncomplete = () => resolve(result);
    transaction.onerror = () => reject(transaction.error ?? new Error("IndexedDB transaction failed"));
    transaction.onabort = () => reject(transaction.error ?? new Error("IndexedDB transaction aborted"));
  });
}

function transactionComplete(transaction: IDBTransaction): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(transaction.error ?? new Error("IndexedDB transaction failed"));
    transaction.onabort = () => reject(transaction.error ?? new Error("IndexedDB transaction aborted"));
  });
}
