import assert from "node:assert/strict";
import test from "node:test";
import { Window } from "happy-dom";
import { Editor } from "@tiptap/core";
import { createDocument } from "../../core/dist/index.js";
import {
  createPlainTextExtensions,
  plainTextDocument,
  readPlainText,
  TiptapBinding
} from "../dist/index.js";

const browserWindow = new Window();
Object.assign(globalThis, {
  window: browserWindow,
  document: browserWindow.document,
  DOMParser: browserWindow.DOMParser,
  MutationObserver: browserWindow.MutationObserver,
  Node: browserWindow.Node,
  HTMLElement: browserWindow.HTMLElement,
  requestAnimationFrame: browserWindow.requestAnimationFrame.bind(browserWindow),
  cancelAnimationFrame: browserWindow.cancelAnimationFrame.bind(browserWindow),
  getComputedStyle: browserWindow.getComputedStyle.bind(browserWindow)
});
Object.defineProperty(globalThis, "navigator", {
  configurable: true,
  value: browserWindow.navigator
});

function createFakeEditor(initialText) {
  const listeners = new Set();
  const state = {
    doc: createFakeDocument(initialText),
    selection: { anchor: 1, head: 1 },
    schema: {
      nodeFromJSON: (node) => node
    }
  };
  const editor = {
    state,
    commands: {
      setContent(content) {
        state.doc = createFakeDocument(jsonToText(content));
        state.selection = { anchor: 1, head: 1 };
        return true;
      },
      setTextSelection(selection) {
        state.selection = { anchor: selection.from, head: selection.to };
        return true;
      }
    },
    on(event, callback) {
      assert.equal(event, "update");
      listeners.add(callback);
    },
    off(event, callback) {
      assert.equal(event, "update");
      listeners.delete(callback);
    },
    updateText(text) {
      state.doc = createFakeDocument(text);
      for (const listener of listeners) {
        listener({ editor });
      }
    }
  };
  return editor;
}

function createFakeDocument(text) {
  return {
    childCount: 1,
    firstChild: { type: { name: "paragraph" } },
    content: { size: text.length + 2 },
    textContent: text,
    textBetween: (_from, _to, _blockSeparator, _leafText) => text
  };
}

function jsonToText(document) {
  return document.content[0].content
    .map((node) => node.type === "hardBreak" ? "\n" : node.text)
    .join("");
}

test("encodes line breaks and Unicode as plain text, without rich-text marks", () => {
  const content = plainTextDocument("A😀\n\nB\n");
  assert.deepEqual(content, {
    type: "doc",
    content: [{
      type: "paragraph",
      content: [
        { type: "text", text: "A😀" },
        { type: "hardBreak" },
        { type: "hardBreak" },
        { type: "text", text: "B" },
        { type: "hardBreak" }
      ]
    }]
  });
});

test("local TipTap edits become CRDT operations, including line breaks", () => {
  const document = createDocument("local");
  const editor = createFakeEditor("");
  const submitted = [];
  const binding = new TiptapBinding({
    editor,
    document,
    onOperations: (operations) => submitted.push(...operations),
    onError: (error) => {
      throw error;
    }
  });

  editor.updateText("Hello😀\nSyncForge");

  assert.equal(document.render(), "Hello😀\nSyncForge");
  assert.equal(submitted.length, Array.from("Hello😀\nSyncForge").length);
  binding.dispose();
});

test("remote edits update the editor without being re-submitted and preserve cursor position", () => {
  const document = createDocument("local");
  document.insertAt(0, "abc");
  const editor = createFakeEditor("abc");
  editor.state.selection = { anchor: 3, head: 3 };
  const submitted = [];
  const binding = new TiptapBinding({
    editor,
    document,
    onOperations: (operations) => submitted.push(...operations),
    onError: (error) => {
      throw error;
    }
  });

  document.insertAt(1, "X");
  binding.setText(document.render());

  assert.equal(readPlainText(editor), "aXbc");
  assert.equal(editor.state.selection.anchor, 4);
  assert.deepEqual(submitted, []);
  binding.dispose();
});

test("binding rejects unsupported multi-paragraph editors and detaches on dispose", () => {
  const document = createDocument("local");
  const editor = createFakeEditor("");
  editor.state.doc = {
    ...editor.state.doc,
    childCount: 2
  };
  assert.throws(() => new TiptapBinding({
    editor,
    document,
    onOperations: () => {},
    onError: (error) => {
      throw error;
    }
  }), /one paragraph/);

  editor.state.doc = createFakeDocument("");
  const binding = new TiptapBinding({
    editor,
    document,
    onOperations: () => {
      throw new Error("must not run after disposal");
    },
    onError: (error) => {
      throw error;
    }
  });
  binding.dispose();
  assert.doesNotThrow(() => editor.updateText("detached"));
  assert.equal(document.render(), "");
});

test("real TipTap editor synchronizes typing, Enter, and multiline paste", () => {
  const element = browserWindow.document.createElement("div");
  browserWindow.document.body.append(element);
  const editor = new Editor({
    element,
    extensions: createPlainTextExtensions(),
    content: plainTextDocument(""),
    editorProps: { attributes: { "aria-label": "Test document" } }
  });
  const document = createDocument("browser-tab");
  const binding = new TiptapBinding({
    editor,
    document,
    onOperations: () => {},
    onError: (error) => {
      throw error;
    }
  });

  editor.commands.insertContent("Hello😀");
  const enterEvent = new browserWindow.KeyboardEvent("keydown", { key: "Enter" });
  const handledEnter = editor.view.someProp("handleKeyDown", (handler) => handler(editor.view, enterEvent));
  assert.equal(handledEnter, true);
  editor.commands.insertContent("world");
  assert.equal(document.render(), "Hello😀\nworld");

  const pastedText = " line\nbreak";
  const pasteEvent = {
    clipboardData: { getData: (type) => type === "text/plain" ? pastedText : "" },
    preventDefault() {}
  };
  const handledPaste = editor.view.someProp("handlePaste", (handler) => handler(editor.view, pasteEvent));
  assert.equal(handledPaste, true);
  assert.equal(document.render(), "Hello😀\nworld line\nbreak");
  assert.equal(readPlainText(editor), document.render());

  binding.dispose();
  editor.destroy();
  element.remove();
});
