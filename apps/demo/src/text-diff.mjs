export function applyTextChange(document, previousText, nextText) {
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
  const operations = [];
  if (deletedLength > 0) {
    operations.push(...document.deleteAt(prefixLength, deletedLength));
  }
  if (inserted.length > 0) {
    operations.push(...document.insertAt(prefixLength, inserted));
  }
  return operations;
}
