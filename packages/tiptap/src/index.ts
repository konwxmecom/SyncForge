import type { Editor, JSONContent } from "@tiptap/core";
import { Extension } from "@tiptap/core";
import Document from "@tiptap/extension-document";
import HardBreak from "@tiptap/extension-hard-break";
import Paragraph from "@tiptap/extension-paragraph";
import Text from "@tiptap/extension-text";
import { Fragment, Slice } from "@tiptap/pm/model";
import { Plugin } from "@tiptap/pm/state";
import type { SingleReplicaDocument, TextOperation } from "@syncforge/core";

export interface TiptapBindingOptions {
  editor: Editor;
  document: SingleReplicaDocument;
  onOperations: (operations: readonly TextOperation[]) => void | Promise<void>;
  onError: (error: Error) => void;
}

const PlainTextInput = Extension.create({
  name: "syncForgePlainTextInput",

  addKeyboardShortcuts() {
    return {
      Enter: () => this.editor.commands.setHardBreak()
    };
  },

  addProseMirrorPlugins() {
    return [
      new Plugin({
        props: {
          handlePaste: (view, event) => {
            const text = event.clipboardData?.getData("text/plain");
            if (text === undefined) {
              return true;
            }
            if (text.length === 0) {
              return true;
            }

            const normalized = text.replace(/\r\n?/g, "\n");
            const nodes = inlineContent(view.state.schema, normalized);
            const transaction = view.state.tr.replaceSelection(new Slice(Fragment.fromArray(nodes), 0, 0));
            view.dispatch(transaction.scrollIntoView());
            return true;
          }
        }
      })
    ];
  }
});

export function createPlainTextExtensions() {
  return [Document, Paragraph, Text, HardBreak, PlainTextInput];
}

export class TiptapBinding {
  private applyingRemoteText = false;
  private disposed = false;
  private readonly handleUpdate = ({ editor }: { editor: Editor }): void => {
    if (this.applyingRemoteText || this.disposed) {
      return;
    }

    try {
      const currentText = readPlainText(editor);
      const operations = applyTextChange(this.options.document, this.options.document.render(), currentText);
      if (operations.length === 0) {
        return;
      }
      void Promise.resolve(this.options.onOperations(operations)).catch((error: unknown) => {
        this.options.onError?.(asError(error));
      });
    } catch (error) {
      this.options.onError?.(asError(error));
    }
  };

  constructor(private readonly options: TiptapBindingOptions) {
    assertPlainTextDocument(options.editor);
    options.editor.on("update", this.handleUpdate);
    this.setText(options.document.render());
  }

  setText(text: string): void {
    if (this.disposed) {
      throw new Error("Cannot update a disposed TipTap binding");
    }
    const editor = this.options.editor;
    if (readPlainText(editor) === text) {
      return;
    }

    const selection = editor.state.selection;
    const previousText = readPlainText(editor);
    const change = textChange(previousText, text);
    const anchor = mapTextOffset(previousText, text, selection.anchor - 1, change);
    const head = mapTextOffset(previousText, text, selection.head - 1, change);
    this.applyingRemoteText = true;
    try {
      if (!editor.commands.setContent(plainTextDocument(text), { emitUpdate: false })) {
        throw new Error("TipTap rejected a synchronized plain-text document");
      }
      const maximumPosition = editor.state.doc.content.size - 1;
      if (!editor.commands.setTextSelection({
        from: Math.max(1, Math.min(anchor + 1, maximumPosition)),
        to: Math.max(1, Math.min(head + 1, maximumPosition))
      })) {
        throw new Error("TipTap rejected the synchronized selection");
      }
    } finally {
      this.applyingRemoteText = false;
    }
  }

  dispose(): void {
    if (this.disposed) {
      return;
    }
    this.disposed = true;
    this.options.editor.off("update", this.handleUpdate);
  }
}

export function plainTextDocument(text: string): JSONContent {
  return {
    type: "doc",
    content: [{
      type: "paragraph",
      content: inlineJSON(text)
    }]
  };
}

export function readPlainText(editor: Editor): string {
  assertPlainTextDocument(editor);
  return editor.state.doc.textBetween(0, editor.state.doc.content.size, "", "\n");
}

function assertPlainTextDocument(editor: Editor): void {
  const document = editor.state.doc;
  if (document.childCount !== 1 || document.firstChild?.type.name !== "paragraph") {
    throw new TypeError("TipTap binding requires one paragraph with hard breaks for newlines");
  }
}

function inlineJSON(text: string): JSONContent[] {
  const content: JSONContent[] = [];
  const lines = text.split("\n");
  lines.forEach((line, index) => {
    if (line.length > 0) {
      content.push({ type: "text", text: line });
    }
    if (index < lines.length - 1) {
      content.push({ type: "hardBreak" });
    }
  });
  return content;
}

function inlineContent(schema: Editor["schema"], text: string) {
  return inlineJSON(text).map((content) => schema.nodeFromJSON(content));
}

function applyTextChange(
  document: SingleReplicaDocument,
  previousText: string,
  nextText: string
): TextOperation[] {
  const { prefixLength, deletedLength, inserted } = textChange(previousText, nextText);
  const operations: TextOperation[] = [];
  if (deletedLength > 0) {
    operations.push(...document.deleteAt(prefixLength, deletedLength));
  }
  if (inserted.length > 0) {
    operations.push(...document.insertAt(prefixLength, inserted));
  }
  return operations;
}

function textChange(previousText: string, nextText: string) {
  const previous = Array.from(previousText);
  const next = Array.from(nextText);
  let prefixLength = 0;
  while (
    prefixLength < previous.length
    && prefixLength < next.length
    && previous[prefixLength] === next[prefixLength]
  ) {
    prefixLength += 1;
  }

  let suffixLength = 0;
  while (
    suffixLength < previous.length - prefixLength
    && suffixLength < next.length - prefixLength
    && previous[previous.length - 1 - suffixLength] === next[next.length - 1 - suffixLength]
  ) {
    suffixLength += 1;
  }

  const deletedLength = previous.length - prefixLength - suffixLength;
  const inserted = next.slice(prefixLength, next.length - suffixLength).join("");
  return { prefixLength, deletedLength, inserted };
}

function mapTextOffset(
  previousText: string,
  nextText: string,
  editorOffset: number,
  change: ReturnType<typeof textChange>
): number {
  const boundedOffset = Math.max(0, Math.min(editorOffset, previousText.length));
  const codePointOffset = Array.from(previousText.slice(0, boundedOffset)).length;
  const insertionLength = Array.from(change.inserted).length;
  let mappedOffset: number;
  if (codePointOffset <= change.prefixLength) {
    mappedOffset = codePointOffset;
  } else if (codePointOffset >= change.prefixLength + change.deletedLength) {
    mappedOffset = codePointOffset + insertionLength - change.deletedLength;
  } else {
    mappedOffset = change.prefixLength + insertionLength;
  }
  return Array.from(nextText).slice(0, mappedOffset).join("").length;
}

function asError(error: unknown): Error {
  return error instanceof Error ? error : new Error(String(error));
}
