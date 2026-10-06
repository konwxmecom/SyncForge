import test from 'node:test';
import assert from 'node:assert/strict';
import { createDocument } from '../dist/index.js';

function snapshotContainsDeleted(snapshot, value) {
  return snapshot.elements.some((element) => element.value === value && element.deleted);
}

test('single-replica document inserts text at start, middle, and end', () => {
  const document = createDocument('replica-a');

  document.insertAt(0, 'b');
  document.insertAt(0, 'a');
  document.insertAt(2, 'c');

  assert.equal(document.render(), 'abc');
});

test('delete marks elements as tombstones but preserves identity', () => {
  const document = createDocument('replica-a');
  document.insertAt(0, 'abc');
  document.deleteAt(1, 1);

  const snapshot = document.snapshot();

  assert.equal(document.render(), 'ac');
  assert.equal(snapshot.elements.length, 3);
  assert.equal(snapshotContainsDeleted(snapshot, 'b'), true);
});

test('snapshot and restore recover visible state and ids', () => {
  const document = createDocument('replica-a');
  document.insertAt(0, 'xyz');

  const snapshot = document.snapshot();
  const restored = createDocument('replica-b');
  restored.restore(snapshot);

  assert.equal(restored.render(), 'xyz');
  assert.deepEqual(restored.snapshot(), snapshot);
});

test('duplicate operations are ignored to preserve invariants', () => {
  const document = createDocument('replica-a');
  const insert = document.insertAt(0, 'ab');

  document.applyOperation(insert[0]);
  document.applyOperation(insert[1]);
  document.applyOperation(insert[0]);

  assert.equal(document.render(), 'ab');
  assert.equal(document.snapshot().elements.length, 2);
});

test('concurrent inserts use the same deterministic sibling order on every replica', () => {
  const seed = createDocument('seed').insertAt(0, 'r')[0];
  const replicaA = createDocument('replica-a');
  const replicaB = createDocument('replica-b');
  replicaA.applyOperation(seed);
  replicaB.applyOperation(seed);

  const fromA = replicaA.insertAt(1, 'A');
  const fromB = replicaB.insertAt(1, 'B');

  replicaA.applyOperation(fromB[0]);
  replicaB.applyOperation(fromA[0]);

  assert.equal(replicaA.render(), 'rAB');
  assert.equal(replicaB.render(), replicaA.render());
});

test('local insertion advances beyond observed remote counters to preserve its position', () => {
  const document = createDocument('replica-a');
  document.applyOperation({ kind: 'insert', id: { replicaId: 'seed', counter: 1 }, after: null, value: 'r' });
  document.applyOperation({
    kind: 'insert',
    id: { replicaId: 'replica-z', counter: 50 },
    after: { replicaId: 'seed', counter: 1 },
    value: 'z'
  });

  document.insertAt(1, 'x');

  assert.equal(document.render(), 'rxz');
  assert.equal(document.snapshot().nextCounter, 51);
});

test('all operation delivery orders converge with missing predecessors and delete-before-insert', () => {
  const operations = [
    { kind: 'insert', id: { replicaId: 'seed', counter: 1 }, after: null, value: 'a' },
    { kind: 'insert', id: { replicaId: 'writer', counter: 1 }, after: { replicaId: 'seed', counter: 1 }, value: 'b' },
    { kind: 'insert', id: { replicaId: 'writer', counter: 2 }, after: { replicaId: 'writer', counter: 1 }, value: 'c' },
    { kind: 'delete', id: { replicaId: 'writer', counter: 1 } }
  ];

  function permutations(values) {
    if (values.length <= 1) {
      return [values];
    }
    return values.flatMap((value, index) =>
      permutations(values.filter((_, candidate) => candidate !== index)).map((rest) => [value, ...rest])
    );
  }

  const results = permutations(operations).map((deliveryOrder) => {
    const document = createDocument('receiver');
    deliveryOrder.forEach((operation) => document.applyOperation(operation));
    return document.render();
  });

  assert.equal(new Set(results).size, 1);
  assert.equal(results[0], 'ac');
});

test('generated independent edits converge after shuffled delivery', () => {
  let randomState = 0x51f0;
  const random = (limit) => {
    randomState = (randomState * 1664525 + 1013904223) >>> 0;
    return randomState % limit;
  };

  for (let run = 0; run < 40; run += 1) {
    const seedOperation = createDocument('seed').insertAt(0, 's')[0];
    const operationLog = [seedOperation];

    for (const replicaId of ['a', 'b', 'c']) {
      const replica = createDocument(replicaId);
      replica.applyOperation(seedOperation);
      for (let step = 0; step < 12; step += 1) {
        const text = replica.render();
        if (text.length > 0 && random(3) === 0) {
          operationLog.push(...replica.deleteAt(random(text.length)));
        } else {
          operationLog.push(...replica.insertAt(random(text.length + 1), String.fromCharCode(97 + random(26))));
        }
      }
    }

    const results = [];
    for (let observer = 0; observer < 3; observer += 1) {
      const shuffled = [...operationLog];
      for (let index = shuffled.length - 1; index > 0; index -= 1) {
        const swapIndex = random(index + 1);
        [shuffled[index], shuffled[swapIndex]] = [shuffled[swapIndex], shuffled[index]];
      }

      const document = createDocument(`observer-${observer}`);
      shuffled.forEach((operation) => document.applyOperation(operation));
      results.push(document.snapshot());
    }

    const canonicalState = (snapshot) => JSON.stringify({
      elements: snapshot.elements,
      pendingDeletes: snapshot.pendingDeletes
    });
    assert.equal(new Set(results.map(canonicalState)).size, 1, `replicas diverged in generated run ${run}`);
  }
});

test('snapshot preserves unresolved inserts and deletes awaiting an insert', () => {
  const document = createDocument('receiver');
  const missingParent = { replicaId: 'remote', counter: 1 };
  document.applyOperation({ kind: 'insert', id: { replicaId: 'remote', counter: 2 }, after: missingParent, value: 'x' });
  document.applyOperation({ kind: 'delete', id: { replicaId: 'remote', counter: 3 } });

  const restored = createDocument('other');
  restored.restore(document.snapshot());
  assert.equal(restored.render(), '');
  restored.applyOperation({ kind: 'insert', id: missingParent, after: null, value: 'p' });
  restored.applyOperation({ kind: 'insert', id: { replicaId: 'remote', counter: 3 }, after: missingParent, value: 'd' });

  assert.equal(restored.render(), 'px');
  assert.equal(restored.snapshot().pendingDeletes.length, 0);
});

test('rejects insertion cycles when a missing predecessor arrives', () => {
  const document = createDocument('receiver');
  const first = { replicaId: 'remote', counter: 1 };
  const second = { replicaId: 'remote', counter: 2 };

  document.applyOperation({ kind: 'insert', id: first, after: second, value: 'a' });
  assert.throws(
    () => document.applyOperation({ kind: 'insert', id: second, after: first, value: 'b' }),
    /predecessor cycle/
  );
  assert.equal(document.render(), '');
});
