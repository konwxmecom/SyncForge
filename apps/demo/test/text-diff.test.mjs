import assert from "node:assert/strict";
import test from "node:test";
import { createDocument } from "../../../packages/core/dist/index.js";
import { applyTextChange } from "../src/text-diff.mjs";

test("converts insertions, removals, and replacements into CRDT operations", () => {
  const document = createDocument("diff-test");
  const initial = document.insertAt(0, "hello world");

  const inserted = applyTextChange(document, "hello world", "hello brave world");
  assert.equal(document.render(), "hello brave world");
  assert.equal(inserted.length, 6);

  const replaced = applyTextChange(document, "hello brave world", "hello kind world");
  assert.equal(document.render(), "hello kind world");
  assert.ok(replaced.length > 0);

  const removed = applyTextChange(document, "hello kind world", "hello world");
  assert.equal(document.render(), "hello world");
  assert.equal(removed.length, 5);
  assert.ok(initial.length > 0);
});

test("diff positions count Unicode code points rather than UTF-16 code units", () => {
  const document = createDocument("unicode-diff");
  document.insertAt(0, "A😀B");

  applyTextChange(document, "A😀B", "A!😀B");

  assert.equal(document.render(), "A!😀B");
});

test("supports complete text replacement and empty-to-empty changes", () => {
  const document = createDocument("replace-diff");
  document.insertAt(0, "old text");

  applyTextChange(document, "old text", "new");
  assert.equal(document.render(), "new");
  assert.deepEqual(applyTextChange(document, "new", "new"), []);
});
