/** Completed request-scoped search response for composable contract tests. */
export function searchStream(groups: unknown[]): ReadableStream<Uint8Array> {
  const bytes = new TextEncoder().encode(`event: search\ndata: ${JSON.stringify({ groups, pendingSources: [], done: true })}\n\n`)
  return new ReadableStream({ start(controller) { controller.enqueue(bytes); controller.close() } })
}
