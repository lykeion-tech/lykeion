/**
 * The references library's objects and the rules every implementation
 * shares (spec 2026-09-16, D-1, D-5, D-11, D-12).
 *
 * A `Paper` is lab-wide and identified by its ids — never by its title. A
 * `Reference` is one Research's attachment of it. The rules here are pure
 * so the lab and the daemon agree on what "the same
 * paper" and "a matching title" mean without any of them holding a copy.
 */

export interface PaperIds {
  doi?: string;
  pmid?: string;
  pmcid?: string;
  arxiv?: string;
  openalex?: string;
}

export type ResolverStatus = "matched" | "unmatched" | "unreachable" | "skipped";
export type ResolverName = "crossref" | "openalex" | "pubmed" | "arxiv";

export interface PaperVerification {
  ts: number;
  resolvers: Record<ResolverName, { status: ResolverStatus; queriedBy?: "id" | "title" }>;
  /** `verified` — any resolver matched. `not-found` — none matched AND at
   *  least one id-keyed lookup was `unmatched`. `unresolvable` — none matched
   *  and no id-keyed lookup missed: a coverage gap, never an accusation. */
  verdict: "verified" | "not-found" | "unresolvable";
  /** The title an id resolved to when it failed `titlesAgree` — the
   *  `doi-mismatch` fact, kept for the row and the Reviewer. */
  resolvedTitle?: string;
}

export interface PaperRetraction {
  ts: number;
  status: "retracted" | "concern" | "corrected" | "none";
  sources: {
    openalex?: "retracted" | "none";
    crossref?: "retracted" | "concern" | "corrected" | "none";
  };
}

export type FullTextRoute = "arxiv" | "unpaywall" | "pmc" | "biorxiv" | "europepmc" | "crossref-link";

export interface PaperFullText {
  route: FullTextRoute;
  ts: number;
  sha256: string;
  contentType: "application/pdf" | "application/xml";
  size: number;
  /** As the route stated it (`cc-by`, a URL); absent when it stated none. */
  licence?: string;
  pageCount?: number;
  pageCountAgrees?: boolean;
  /** The extracted text's own blob. */
  textSha256: string;
  textChars: number;
}

export interface PaperAuthor {
  family: string;
  given?: string;
}

export type MetadataSource = "index" | "import" | "agent";

export interface Paper {
  id: string;
  ids: PaperIds;
  title: string;
  authors: PaperAuthor[];
  year?: number;
  venue?: string;
  /** The index's own label (`journal-article`, `preprint`, `book`…),
   *  stored as an observation and never inferred. */
  type?: string;
  abstract?: string;
  metadata: { source: MetadataSource; ts: number };
  verification?: PaperVerification;
  retraction?: PaperRetraction;
  fullText?: PaperFullText;
  /** `normaliseTitle(title)`, for `suggestedMatches` only. */
  titleKey: string;
  createdTs: number;
  updatedTs: number;
}

/** What a capture or an import brings: a Paper without the lab's own
 *  fields. `ids` should already be normalised by the caller. */
export type PaperInput = Omit<Paper, "id" | "titleKey" | "createdTs" | "updatedTs">;

export type ReadLevel = "full-text" | "sections" | "abstract" | "none";

export interface Reference {
  id: string;
  researchId: string;
  paperId: string;
  /** Unique within the Research (D-11). */
  citeKey: string;
  notes?: string;
  addedBy: { kind: "agent"; taskId: string; runId: string } | { kind: "import" };
  addedTs: number;
  /** Automatic — one entry per Task whose agent retrieved the text. */
  readBy: { taskId: string; ts: number }[];
  /** The researcher's own declaration; never set by capture (D-6). */
  read?: { level: ReadLevel; userId: string; ts: number };
}

export interface LiteratureSearch {
  id: string;
  researchId: string;
  taskId: string;
  runId: string;
  tool: "search_works" | "search_articles" | "find_related_articles";
  query: string;
  hits?: number;
  ts: number;
}

/** What `listReferences` answers: the join the surfaces draw. */
export interface ReferenceRow {
  reference: Reference;
  paper: Paper;
  /** Filled only for a Paper whose `ids` is empty and whose `titleKey` and
   *  `year` equal another Paper's (D-1). */
  suggestedMatches?: { paperId: string; title: string }[];
}

export type ImportFormat = "bibtex" | "ris" | "csl-json";

export type ImportRejectionReason =
  | "no-title"
  | "bad-year"
  | "unparseable"
  | "duplicate-in-file"
  | "already-listed";

export interface ImportRejection {
  /** The entry as the file named it — its key, or its first line. */
  entry: string;
  reason: ImportRejectionReason;
  detail?: string;
}

export interface ImportResult {
  imported: Reference[];
  rejected: ImportRejection[];
}

/** `importReferences` refuses text over this. */
export const IMPORT_MAX_BYTES = 5 * 1024 * 1024;

const DOI = /^10\.\d{4,9}\/\S+$/;

/** The rules `Paper.ids` states: DOI lower-cased without its resolver prefix,
 *  arXiv without its version, PMCID with its prefix, OpenAlex upper-cased.
 *  A value that is not one of its kind is dropped, never kept wrong. */
export function normaliseIds(ids: PaperIds): PaperIds {
  const out: PaperIds = {};
  if (ids.doi !== undefined) {
    const doi = ids.doi
      .trim()
      .replace(/^https?:\/\/(?:dx\.)?doi\.org\//i, "")
      .replace(/^doi:\s*/i, "")
      .toLowerCase();
    if (DOI.test(doi)) out.doi = doi;
  }
  if (ids.pmid !== undefined) {
    const m = /^\s*(?:pmid:\s*)?(\d+)\s*$/i.exec(ids.pmid);
    if (m) out.pmid = m[1]!;
  }
  if (ids.pmcid !== undefined) {
    const m = /^\s*(?:pmc)?(\d+)\s*$/i.exec(ids.pmcid);
    if (m) out.pmcid = `PMC${m[1]}`;
  }
  if (ids.arxiv !== undefined) {
    const m = /^\s*(?:arxiv:\s*)?([a-z-]+(?:\.[a-z]{2})?\/\d{7}|\d{4}\.\d{4,5})(?:v\d+)?\s*$/i.exec(ids.arxiv);
    if (m) out.arxiv = m[1]!.toLowerCase();
  }
  if (ids.openalex !== undefined) {
    const m = /^\s*(?:https?:\/\/openalex\.org\/)?(w\d+)\s*$/i.exec(ids.openalex);
    if (m) out.openalex = m[1]!.toUpperCase();
  }
  return out;
}

/** Lower-cased, marks stripped (NFKD), punctuation to spaces, whitespace
 *  collapsed. What `titleKey` holds and every title rule compares. */
export function normaliseTitle(title: string): string {
  return title
    .normalize("NFKD")
    .replace(/\p{M}/gu, "")
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, " ")
    .trim();
}

/** Longest common subsequence over words, as a ratio of the two lengths —
 *  the same measure the plan diff's `diffWords` computes. 0 when either
 *  side is empty. */
export function titleSimilarity(a: string, b: string): number {
  const x = normaliseTitle(a).split(" ").filter(Boolean);
  const y = normaliseTitle(b).split(" ").filter(Boolean);
  if (x.length === 0 || y.length === 0) return 0;
  const table: number[][] = Array.from({ length: x.length + 1 }, () => new Array<number>(y.length + 1).fill(0));
  for (let i = 1; i <= x.length; i++)
    for (let j = 1; j <= y.length; j++)
      table[i]![j] = x[i - 1] === y[j - 1] ? table[i - 1]![j - 1]! + 1 : Math.max(table[i - 1]![j]!, table[i]![j - 1]!);
  return (2 * table[x.length]![y.length]!) / (x.length + y.length);
}

/** Titles an index answers for many unrelated works; a lookup BY TITLE can
 *  never match on one of these alone. */
export const GENERIC_TITLES: ReadonlySet<string> = new Set([
  "editorial", "erratum", "corrigendum", "letter", "reply", "case report",
  "introduction", "preface", "abstract", "untitled",
]);

/** For a record found BY ID: the title it carries is close enough to the
 *  one we asked about. Below this the id resolved to a different paper. */
export function titlesAgree(a: string, b: string): boolean {
  return titleSimilarity(a, b) >= 0.7;
}

/** For a record found BY TITLE: exact normalised equality, nothing less,
 *  and never for a generic title. */
export function titleLookupMatches(a: string, b: string): boolean {
  const na = normaliseTitle(a);
  if (GENERIC_TITLES.has(na) || GENERIC_TITLES.has(normaliseTitle(b))) return false;
  return na === normaliseTitle(b);
}

const STOPWORDS = new Set([
  "a", "an", "the", "of", "on", "in", "for", "to", "and", "or", "with", "by",
  "from", "at", "as", "is", "are", "be", "into", "toward", "towards", "via",
  "using", "about", "over", "under", "between", "through", "during",
]);

const ascii = (s: string) => s.normalize("NFKD").replace(/\p{M}/gu, "").toLowerCase().replace(/[^a-z]/g, "");

/** `a`, `b`, …, `z`, `aa`, `ab`, … for n = 1, 2, …; "" for 0. */
function suffix(n: number): string {
  let out = "";
  let k = n;
  while (k > 0) {
    k -= 1;
    out = String.fromCharCode(97 + (k % 26)) + out;
    k = Math.floor(k / 26);
  }
  return out;
}

/** `{family}{year}{firstword}`, lower-cased ASCII, `anon` / `nd` for what is
 *  missing, the first title word that is not a stopword and starts with a
 *  letter, then `a`, `b`… until the key is not in `taken` (D-11). */
export function citeKeyFor(
  paper: { authors: PaperAuthor[]; year?: number; title: string },
  taken: ReadonlySet<string>,
): string {
  const family = ascii(paper.authors[0]?.family ?? "") || "anon";
  const year = paper.year === undefined ? "nd" : String(paper.year);
  const word =
    normaliseTitle(paper.title)
      .split(" ")
      .find((w) => /^[a-z]/.test(w) && !STOPWORDS.has(w)) ?? "";
  const base = `${family}${year}${ascii(word)}`;
  for (let n = 0; ; n++) {
    const key = `${base}${suffix(n)}`;
    if (!taken.has(key)) return key;
  }
}

export function verdictOf(resolvers: PaperVerification["resolvers"]): PaperVerification["verdict"] {
  const answers = Object.values(resolvers);
  if (answers.some((r) => r.status === "matched")) return "verified";
  if (answers.some((r) => r.status === "unmatched" && r.queriedBy === "id")) return "not-found";
  return "unresolvable";
}

export function retractionOf(sources: PaperRetraction["sources"]): PaperRetraction["status"] {
  if (sources.openalex === "retracted" || sources.crossref === "retracted") return "retracted";
  if (sources.crossref === "concern" || sources.crossref === "corrected") return sources.crossref;
  return "none";
}

/** The one heuristic badge: a preprint dated 2024 or later. */
export function preprintAfterInflection(paper: { type?: string; year?: number }): boolean {
  return paper.type === "preprint" && (paper.year ?? 0) >= 2024;
}

const RANK: Record<MetadataSource, number> = { index: 3, import: 2, agent: 1 };

const METADATA_FIELDS = ["title", "authors", "year", "venue", "type", "abstract"] as const;

/**
 * One Paper, twice reported (D-1). Ids are unioned and a held id wins over
 * an incoming one of the same kind; a metadata field is filled when empty
 * and overwritten only by a higher-ranked source; `verification` and
 * `retraction` take the newer `ts`; `fullText` is kept unless absent, or the
 * incoming is a PDF and the held one is XML. Never mutates.
 */
export function mergePaper(existing: Paper, incoming: PaperInput): Paper {
  const out: Paper = { ...existing, ids: { ...existing.ids }, authors: [...existing.authors] };
  for (const [kind, value] of Object.entries(incoming.ids) as [keyof PaperIds, string | undefined][])
    if (value !== undefined && out.ids[kind] === undefined) out.ids[kind] = value;
  const higher = RANK[incoming.metadata.source] > RANK[existing.metadata.source];
  for (const field of METADATA_FIELDS) {
    const held = existing[field];
    const offered = incoming[field];
    const empty = held === undefined || held === "" || (Array.isArray(held) && held.length === 0);
    const offeredEmpty = offered === undefined || offered === "" || (Array.isArray(offered) && offered.length === 0);
    if (offeredEmpty) continue;
    if (empty || higher) (out as unknown as Record<string, unknown>)[field] = Array.isArray(offered) ? [...offered] : offered;
  }
  if (higher) out.metadata = { ...incoming.metadata };
  if (incoming.verification && (!existing.verification || incoming.verification.ts >= existing.verification.ts))
    out.verification = incoming.verification;
  if (incoming.retraction && (!existing.retraction || incoming.retraction.ts >= existing.retraction.ts))
    out.retraction = incoming.retraction;
  if (
    incoming.fullText &&
    (!existing.fullText ||
      (incoming.fullText.contentType === "application/pdf" && existing.fullText.contentType === "application/xml"))
  )
    out.fullText = incoming.fullText;
  out.titleKey = normaliseTitle(out.title);
  return out;
}
