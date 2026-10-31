/**
 * Reading one build's SyncTeX.
 *
 * The lab serves the file exactly as Tectonic wrote it — gzip, under
 * `application/gzip` and with no `Content-Encoding` — so nothing unpacks it
 * on the way in and this is where it is unpacked. What the records MEAN is
 * `parseSynctex`'s, tested in `@lykeion/api` against a real Tectonic run;
 * what is pinned here is the fetch, the gunzip and the refusal.
 */
import { gzipSync } from "node:zlib";
import { afterEach, expect, it, vi } from "vitest";
import { fetchSynctex } from "./synctex-fetch";

/**
 * A SyncTeX file in the shape Tectonic writes one: the `Input:` table naming
 * each source by the absolute path of the tree the build compiled, then a
 * block per page. Cut to the records these tests read.
 */
const SYNCTEX = [
  "SyncTeX Version:1",
  "Input:1:/build/tree/main.tex",
  "Input:2:/build/tree/sections/results.tex",
  "Output:pdf",
  "Magnification:1000",
  "Unit:1",
  "X Offset:0",
  "Y Offset:0",
  "Content:",
  "{1",
  "h1,7:4736286,4718592:19144640,655360,0",
  "}",
  "{2",
  "h1,21:4736286,26214400:19144640,655360,0",
  "x2,4:5938190,9175040",
  "}",
  "Postamble:",
  "Count:3",
  "",
].join("\n");

const HREF = "/tasks/t_paper/manuscript/builds/4.synctex.gz";

afterEach(() => vi.unstubAllGlobals());

it("gunzips a build's SyncTeX and answers the records in it", async () => {
  const fetched = vi.fn().mockResolvedValue(
    new Response(new Uint8Array(gzipSync(Buffer.from(SYNCTEX))), {
      headers: { "content-type": "application/gzip" },
    }),
  );
  vi.stubGlobal("fetch", fetched);

  const records = await fetchSynctex(HREF);

  expect(fetched).toHaveBeenCalledWith(HREF);
  expect(records).toEqual([
    { page: 1, x: 4736286, y: 4718592, file: "/build/tree/main.tex", line: 7 },
    { page: 2, x: 4736286, y: 26214400, file: "/build/tree/main.tex", line: 21 },
    { page: 2, x: 5938190, y: 9175040, file: "/build/tree/sections/results.tex", line: 4 },
  ]);
});

it("refuses a build the lab has no SyncTeX for, saying what it answered", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(new Response("this build has no such file", { status: 404 })),
  );

  await expect(fetchSynctex(HREF)).rejects.toThrow("404");
});
