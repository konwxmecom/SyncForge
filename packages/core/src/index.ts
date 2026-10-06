export interface ElementId {
  replicaId: string;
  counter: number;
}

export interface TextElement {
  id: ElementId;
  after: ElementId | null;
  value: string;
  deleted: boolean;
}

export interface InsertOperation {
  kind: "insert";
  id: ElementId;
  after: ElementId | null;
  value: string;
}

export interface DeleteOperation {
  kind: "delete";
  id: ElementId;
}

export type TextOperation = InsertOperation | DeleteOperation;

export interface DocumentSnapshot {
  replicaId: string;
  nextCounter: number;
  elements: TextElement[];
  pendingDeletes: ElementId[];
}

function sameId(left: ElementId | null, right: ElementId | null): boolean {
  return left === null || right === null
    ? left === right
    : left.replicaId === right.replicaId && left.counter === right.counter;
}

function compareIds(left: ElementId, right: ElementId): number {
  if (left.counter !== right.counter) {
    return left.counter > right.counter ? -1 : 1;
  }
  return left.replicaId < right.replicaId ? -1 : left.replicaId > right.replicaId ? 1 : 0;
}

function isElementId(value: unknown): value is ElementId {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return false;
  }

  const id = value as Record<string, unknown>;
  return typeof id.replicaId === "string"
    && id.replicaId.length > 0
    && Number.isSafeInteger(id.counter)
    && (id.counter as number) > 0;
}

function idKey(id: ElementId): string {
  return JSON.stringify([id.replicaId, id.counter]);
}

export class SingleReplicaDocument {
  replicaId: string;
  private nextCounter = 0;
  private readonly elements = new Map<string, TextElement>();
  private readonly pendingDeletes = new Set<string>();

  constructor(replicaId = "local") {
    if (replicaId.length === 0) {
      throw new TypeError("replicaId must not be empty");
    }
    this.replicaId = replicaId;
  }

  private nextId(): ElementId {
    this.nextCounter += 1;
    return {
      replicaId: this.replicaId,
      counter: this.nextCounter
    };
  }

  private orderedElements(): TextElement[] {
    const children = new Map<string, TextElement[]>();

    for (const element of this.elements.values()) {
      const parentKey = element.after === null ? "" : idKey(element.after);
      const siblings = children.get(parentKey) ?? [];
      siblings.push(element);
      children.set(parentKey, siblings);
    }

    for (const siblings of children.values()) {
      siblings.sort((left, right) => compareIds(left.id, right.id));
    }

    const result: TextElement[] = [];
    const visited = new Set<string>();
    const roots = children.get("") ?? [];
    const stack = [...roots].reverse();
    while (stack.length > 0) {
      const element = stack.pop()!;
      const key = idKey(element.id);
      if (visited.has(key)) {
        continue;
      }
      visited.add(key);
      result.push(element);
      const descendants = children.get(key) ?? [];
      for (let index = descendants.length - 1; index >= 0; index -= 1) {
        stack.push(descendants[index]);
      }
    }
    return result;
  }

  private visibleElements(): TextElement[] {
    return this.orderedElements().filter((element) => !element.deleted);
  }

  insertAt(position: number, value: string): InsertOperation[] {
    if (!Number.isFinite(position) || typeof value !== "string") {
      throw new TypeError("position must be finite and value must be a string");
    }
    if (value.length === 0) {
      return [];
    }

    const visible = this.visibleElements();
    const clampedPosition = Math.min(Math.max(Math.trunc(position), 0), visible.length);
    let after = clampedPosition === 0 ? null : visible[clampedPosition - 1].id;
    const operations: InsertOperation[] = [];

    for (const character of value) {
      const operation: InsertOperation = {
        kind: "insert",
        id: this.nextId(),
        after: after === null ? null : { ...after },
        value: character
      };
      this.applyOperation(operation);
      operations.push(operation);
      after = operation.id;
    }

    return operations;
  }

  deleteAt(position: number, length = 1): DeleteOperation[] {
    const visible = this.visibleElements();
    if (!Number.isFinite(position) || !Number.isFinite(length) || length <= 0 || position < 0 || position >= visible.length) {
      return [];
    }

    const start = Math.trunc(position);
    const end = Math.min(start + Math.trunc(length), visible.length);
    const operations: DeleteOperation[] = [];

    for (const element of visible.slice(start, end)) {
      const operation: DeleteOperation = { kind: "delete", id: { ...element.id } };
      this.applyOperation(operation);
      operations.push(operation);
    }

    return operations;
  }

  applyOperation(operation: TextOperation): void {
    if (operation.kind === "insert") {
      if (!isElementId(operation.id)
        || (operation.after !== null && !isElementId(operation.after))
        || typeof operation.value !== "string"
        || Array.from(operation.value).length !== 1) {
        throw new TypeError("Invalid insert operation");
      }

      const key = idKey(operation.id);
      if (this.elements.has(key)) {
        return;
      }

      if (operation.after !== null && sameId(operation.id, operation.after)) {
        throw new TypeError("An element cannot reference itself as its predecessor");
      }

      let ancestor = operation.after;
      while (ancestor !== null) {
        if (sameId(operation.id, ancestor)) {
          throw new TypeError("Insert operation would create a predecessor cycle");
        }
        const parent = this.elements.get(idKey(ancestor));
        ancestor = parent?.after ?? null;
      }

      this.elements.set(key, {
        id: { ...operation.id },
        after: operation.after === null ? null : { ...operation.after },
        value: operation.value,
        deleted: this.pendingDeletes.delete(key)
      });
      this.nextCounter = Math.max(this.nextCounter, operation.id.counter);
      return;
    }

    if (operation.kind !== "delete" || !isElementId(operation.id)) {
      throw new TypeError("Invalid delete operation");
    }

    const key = idKey(operation.id);
    const target = this.elements.get(key);
    if (target) {
      target.deleted = true;
    } else {
      this.pendingDeletes.add(key);
    }
  }

  render(): string {
    return this.visibleElements().map((element) => element.value).join("");
  }

  toString(): string {
    return this.render();
  }

  snapshot(): DocumentSnapshot {
    return {
      replicaId: this.replicaId,
      nextCounter: this.nextCounter,
      elements: Array.from(this.elements.values()).sort((left, right) => compareIds(left.id, right.id)).map((element) => ({
        id: { ...element.id },
        after: element.after === null ? null : { ...element.after },
        value: element.value,
        deleted: element.deleted
      })),
      pendingDeletes: Array.from(this.pendingDeletes, (key) => {
        const [replicaId, counter] = JSON.parse(key) as [string, number];
        return { replicaId, counter };
      })
    };
  }

  restore(snapshot: DocumentSnapshot): void {
    if (typeof snapshot.replicaId !== "string"
      || snapshot.replicaId.length === 0
      || !Number.isSafeInteger(snapshot.nextCounter)
      || snapshot.nextCounter < 0
      || !Array.isArray(snapshot.elements)
      || !Array.isArray(snapshot.pendingDeletes)) {
      throw new TypeError("Invalid document snapshot");
    }

    const restoredElements = new Map<string, TextElement>();
    let maxCounter = 0;
    for (const element of snapshot.elements) {
      if (!isElementId(element.id)
        || (element.after !== null && !isElementId(element.after))
        || typeof element.value !== "string"
        || Array.from(element.value).length !== 1
        || typeof element.deleted !== "boolean") {
        throw new TypeError("Invalid document snapshot element");
      }

      const key = idKey(element.id);
      if (restoredElements.has(key)) {
        throw new TypeError("Document snapshot contains duplicate element IDs");
      }
      restoredElements.set(key, {
        id: { ...element.id },
        after: element.after === null ? null : { ...element.after },
        value: element.value,
        deleted: element.deleted
      });
      maxCounter = Math.max(maxCounter, element.id.counter);
    }

    const restoredPendingDeletes = new Set<string>();
    for (const id of snapshot.pendingDeletes) {
      if (!isElementId(id)) {
        throw new TypeError("Invalid pending delete in document snapshot");
      }
      const key = idKey(id);
      if (!restoredElements.has(key)) {
        restoredPendingDeletes.add(key);
      }
    }

    this.replicaId = snapshot.replicaId;
    this.nextCounter = Math.max(snapshot.nextCounter, maxCounter);
    this.elements.clear();
    for (const [key, element] of restoredElements) {
      this.elements.set(key, element);
    }
    this.pendingDeletes.clear();
    for (const key of restoredPendingDeletes) {
      this.pendingDeletes.add(key);
    }
  }
}

export function createDocument(replicaId = "local"): SingleReplicaDocument {
  return new SingleReplicaDocument(replicaId);
}
