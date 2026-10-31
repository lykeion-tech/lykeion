import { parseSynctex, type SynctexRecord } from "@lykeion/api";

/**
 * One build's SyncTeX, read off the route that serves it.
 *
 * The lab hands the file back exactly as Tectonic wrote it — gzip, under
 * `application/gzip` and with no `Content-Encoding` — so no browser unpacks
 * it on the way in and this is where it is unpacked. `DecompressionStream`
 * is the platform's own gunzip: nothing is bundled for it, and the bytes
 * are never held twice, because the response's body streams straight
 * through it.
 *
 * Reading the records is `parseSynctex`'s, which is the contract's and is
 * tested against a real Tectonic run. All that is added here is the fetch
 * and its one refusal: a build with no SyncTeX answers `404`, and a caller
 * that swallowed it would leave Go to PDF looking like a control that does
 * nothing.
 */
export async function fetchSynctex(url: string): Promise<SynctexRecord[]> {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`synctex ${res.status}`);
  // Said as itself rather than as a status: a body-less 200 would otherwise
  // read as `synctex 200`, which names a success in a failure.
  if (res.body === null) throw new Error("synctex answered with no bytes");
  const text = await new Response(res.body.pipeThrough(new DecompressionStream("gzip"))).text();
  return parseSynctex(text);
}
