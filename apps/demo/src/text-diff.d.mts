import type { SingleReplicaDocument, TextOperation } from "@syncforge/core";

export function applyTextChange(
  document: SingleReplicaDocument,
  previousText: string,
  nextText: string
): TextOperation[];
