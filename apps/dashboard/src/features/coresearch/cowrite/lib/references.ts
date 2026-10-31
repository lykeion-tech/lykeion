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

/** What `listReferences` answers: the join the surfaces draw. */
export interface ReferenceRow {
  reference: Reference;
  paper: Paper;
  /** Filled only for a Paper whose `ids` is empty and whose `titleKey` and
   *  `year` equal another Paper's (D-1). */
  suggestedMatches?: { paperId: string; title: string }[];
}

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

/** For a record found BY ID: the title it carries is close enough to the
 *  one we asked about. Below this the id resolved to a different paper. */
export function titlesAgree(a: string, b: string): boolean {
  return titleSimilarity(a, b) >= 0.7;
}
