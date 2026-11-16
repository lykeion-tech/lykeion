import Link from 'next/link';
import { ArrowLeft, ArrowUpRight } from 'lucide-react';
import { notFound } from 'next/navigation';
import { comparisons } from '../content';
import CompareShell from '../compare-shell';
import '../comparison.css';

type Slug = keyof typeof comparisons;
type Props = { params: Promise<{ slug: string }> };
export function generateStaticParams() { return Object.keys(comparisons).map(slug => ({ slug })); }
export async function generateMetadata({ params }: Props) {
  const { slug } = await params;
  const item = comparisons[slug as Slug];
  return item ? { title: `${item.name} vs. Lykeion`, description: item.intro } : {};
}
export default async function ComparisonPage({ params }: Props) {
  const { slug } = await params;
  const item = comparisons[slug as Slug];
  if (!item) notFound();
  return <CompareShell><article className="comparison"><Link className="comparison-back" href="/#compare"><ArrowLeft size={14} aria-hidden="true"/> Back</Link><div className="comparison-kicker">COMPARISON <span>|</span> <time dateTime="2026-11-16">NOV 16, 2026</time></div><h1>{item.name} vs. Lykeion</h1><p className="comparison-intro">{item.intro}</p><div className={`comparison-art comparison-art-${slug}`} aria-label={`${item.name} and Lykeion`}><div className="comparison-mark"><img src={item.icon} alt=""/><span>{item.name}</span></div><span className="comparison-versus">vs.</span><div className="comparison-mark"><img src="/lykeion.svg" alt=""/><span>Lykeion</span></div></div><section><h2>{item.overviewTitle}</h2><p>{item.summary}</p></section><section><h2>{item.tableTitle}</h2><div className="comparison-table-wrap"><table><thead><tr><th scope="col"> </th><th scope="col">{item.name}</th><th scope="col">Lykeion</th></tr></thead><tbody>{item.rows.map(row => <tr key={row[0]}><th scope="row">{row[0]}</th><td>{row[1]}</td><td>{row[2]}</td></tr>)}</tbody></table></div></section>{item.sections.map(section => <section key={section[0]}><h2>{section[0]}</h2>{section.slice(1).map(paragraph => <p key={paragraph}>{paragraph}</p>)}</section>)}<section className="comparison-sources"><h2>Sources.</h2><p>Product information checked November 16, 2026. Features and availability can change.</p><ul><li><a href={item.source}>{item.sourceLabel} <ArrowUpRight size={12} aria-hidden="true"/></a></li><li><a href="https://github.com/lykeion-tech/lykeion">Lykeion: source and documentation <ArrowUpRight size={12} aria-hidden="true"/></a></li></ul></section></article></CompareShell>;
}
