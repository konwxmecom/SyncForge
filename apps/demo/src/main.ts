import { createDocument } from "@syncforge/core";
import { Editor } from "@tiptap/core";
import { IndexedDBSyncStorage, SyncClient, type SyncStatus } from "@syncforge/sync-client";
import {
  createPlainTextExtensions,
  plainTextDocument,
  readPlainText,
  TiptapBinding
} from "@syncforge/tiptap";
import "./style.css";

const editorElement = document.querySelector<HTMLDivElement>("#editor");
const documentHeading = document.querySelector<HTMLElement>("#document-heading");
const workspaceDocumentTitle = document.querySelector<HTMLElement>("#workspace-document-title");
const status = document.querySelector<HTMLElement>("#sync-status");
const statusLabel = document.querySelector<HTMLElement>("#status-label");
const editorHint = document.querySelector<HTMLElement>("#editor-hint");
const characterCount = document.querySelector<HTMLElement>("#character-count");
const replicaLabel = document.querySelector<HTMLElement>("#replica-id");
const notice = document.querySelector<HTMLElement>("#notice");
const copyLink = document.querySelector<HTMLButtonElement>("#copy-link");
const authTokenInput = document.querySelector<HTMLInputElement>("#auth-token");
const connectButton = document.querySelector<HTMLButtonElement>("#connect");

if (!editorElement || !documentHeading || !workspaceDocumentTitle || !status || !statusLabel || !editorHint || !characterCount || !replicaLabel || !notice || !copyLink || !authTokenInput || !connectButton) {
  throw new Error("Demo page is missing required editor elements");
}

const statusElement = status;
const statusLabelElement = statusLabel;
const characterCountElement = characterCount;
const noticeElement = notice;
const authTokenElement = authTokenInput;
let previousText = "";
let sendQueue = Promise.resolve();
let noticeTimer: number | undefined;
let editor: Editor | undefined;
let editorBinding: TiptapBinding | undefined;
const documentId = getDocumentId();
const replicaId = getReplicaId();
const doc = createDocument(replicaId);
const storage = new IndexedDBSyncStorage();
const socketProtocol = window.location.protocol === "https:" ? "wss:" : "ws:";
let client = createSyncClient();

documentHeading.textContent = documentId;
workspaceDocumentTitle.textContent = documentId;
replicaLabel.textContent = replicaId.slice(0, 8);

copyLink.addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(window.location.href);
    showNotice("Invite link copied. Open it in another tab to collaborate.");
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    showNotice(`Could not copy the link: ${message}`);
  }
});

connectButton.addEventListener("click", () => {
  client.close();
  client = createSyncClient();
  void client.connect().catch(() => undefined);
});

window.addEventListener("online", () => {
  if (!client.isConnected) {
    void client.connect().catch(() => undefined);
  }
});

window.addEventListener("offline", () => setStatus("offline"));

let initializationError: Error | undefined;
try {
  await client.initialize();
} catch (error) {
  initializationError = error instanceof Error ? error : new Error(String(error));
}

previousText = doc.render();
editor = new Editor({
  element: editorElement,
  extensions: createPlainTextExtensions(),
  content: plainTextDocument(previousText),
  editable: false,
  editorProps: {
    attributes: {
      "aria-label": "Shared plain-text document",
      spellcheck: "false"
    }
  }
});
editorBinding = new TiptapBinding({
  editor,
  document: doc,
  onOperations: (operations) => {
    sendQueue = sendQueue
      .then(() => client.sendOperations(operations))
      .catch((error: unknown) => {
        const message = error instanceof Error ? error.message : String(error);
        showNotice(`Could not save or queue this edit: ${message}`);
      });
  },
  onError: (error) => showNotice(`Could not apply editor change: ${error.message}`)
});
editor.on("update", ({ editor: updatedEditor }) => {
  previousText = readPlainText(updatedEditor);
  updateCharacterCount(previousText);
});
editor.setEditable(true);
updateCharacterCount(previousText);

if (initializationError) {
  setStatus("offline");
  editorHint.textContent = "Local restore failed; edits may not persist in this browser.";
  showNotice(`Could not initialize local storage: ${initializationError.message}`);
} else {
  editorHint.textContent = "Plain-text edits save locally and sync when the connection is available.";
  try {
    await client.connect();
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    setStatus("offline");
    editorHint.textContent = "You can edit offline; edits will queue for synchronization.";
    showNotice(`Sync connection unavailable: ${message}`);
  }
}

window.addEventListener("pagehide", () => {
  editorBinding?.dispose();
  editor?.destroy();
  client.close();
}, { once: true });

function setStatus(next: SyncStatus): void {
  statusElement.dataset.state = next;
  const labels: Record<SyncStatus, string> = {
    connecting: "Connecting",
    connected: "Live",
    reconnecting: "Reconnecting",
    offline: "Offline · saving here",
    closed: "Disconnected"
  };
  statusLabelElement.textContent = labels[next];
}

function updateCharacterCount(text: string): void {
  const count = Array.from(text).length;
  characterCountElement.textContent = `${count.toLocaleString()} ${count === 1 ? "character" : "characters"}`;
}

function showNotice(message: string): void {
  noticeElement.textContent = message;
  noticeElement.hidden = false;
  if (noticeTimer !== undefined) {
    window.clearTimeout(noticeTimer);
  }
  noticeTimer = window.setTimeout(() => {
    noticeElement.hidden = true;
  }, 6000);
}

function getDocumentId(): string {
  const params = new URLSearchParams(window.location.search);
  const requested = params.get("doc");
  if (requested && /^[A-Za-z0-9._:-]{1,128}$/.test(requested)) {
    return requested;
  }
  if (requested) {
    showNotice("Document ID contains unsupported characters; opened the default demo room.");
  }
  return "demo-room";
}

function getReplicaId(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return `tab-${Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")}`;
}

function createSyncClient(): SyncClient {
  return new SyncClient({
    url: `${socketProtocol}//${window.location.host}/sync`,
    documentId,
    replicaId,
    document: doc,
    storage,
    ...(authTokenElement.value.length === 0 ? {} : { authToken: authTokenElement.value }),
    onChange: (text) => {
      previousText = text;
      editorBinding?.setText(text);
      updateCharacterCount(text);
    },
    onStatus: (nextStatus) => setStatus(nextStatus),
    onError: (error) => {
      setStatus("offline");
      showNotice(error instanceof Error ? error.message : error.message);
    }
  });
}
