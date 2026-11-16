import type { Metadata } from 'next';
import Link from 'next/link';
import { ArrowUpRight } from 'lucide-react';
import SiteFooter from '../site-footer';
import './about.css';

export const metadata: Metadata = { title: 'About · Lykeion', description: 'The thinking and the person behind Lykeion.' };

export default function AboutPage() {
  return <>
    <header className="site-header"><div className="nav-inner"><Link className="brand" href="/" aria-label="Lykeion home"><img src="/lykeion.svg" alt=""/></Link><div className="nav-actions"><a className="github" href="https://github.com/lykeion-tech/lykeion"><img src="/github.svg" width="17" height="17" alt=""/> GitHub</a><a className="nav-login" href="/app/#/signin">Log in</a><a className="pill primary" href="/app/#/signin">Get started</a></div></div></header>
    <main className="about-page"><div className="about-content">
      <h1>About Lykeion</h1>
      <p>Lykeion — (<strong lang="el">Λύκειον</strong> in Greek) is a research lab where agents work and people steer.</p>
      <p>The name is an homage to the Lykeion in ancient Athens: the place where Aristotle founded his school and a community gathered to teach, investigate and learn. The Lykeion is historically recognized as one of the earliest systematic research institutes and scientific schools in the Western world.</p>
      <p>Research today asks people to coordinate an enormous amount of work: finding evidence, developing hypotheses, running experiments and checking results. AI agents can take on more of that work, but a collection of agents still needs a way to work together.</p>
      <p>Lykeion is built around that idea. Researchers set the direction, and agents carry out studies within a shared workspace. Evidence and outputs stay connected to the work, while checks and reviews help decide when it is ready to move forward.</p>
      <p>The future I envision is one where agents can coordinate scientific work over time without a person having to supervise every routine step. Researchers remain free to ask better questions, exercise judgment and decide where to go next.</p>
      <a className="pill primary about-github" href="https://github.com/lykeion-tech/lykeion"><img src="/github.svg" width="16" height="16" alt=""/> View on GitHub <ArrowUpRight size={14} aria-hidden="true"/></a>
      <section className="about-person"><h2><s>Who we are</s> <span>Who am I</span></h2>
        <div className="about-profile"><img src="/founder-avatar.png" alt="" width="72" height="72"/><p><strong>It’s just me!</strong> I’m <a href="https://github.com/alvy-singer">@alvy-singer</a>, a cognitive science student building tools for a more collaborative future of research.</p></div>
        <p>When I realized that research was the path I wanted to follow, I began thinking about what academic collaboration could become in the age of AI. I imagined a lab where agents could carry work forward together, while researchers had the time and space to think, question and guide it.</p>
        <p>I built Lykeion to start making that idea real.</p>
      </section>
      <div className="about-cards" aria-label="Contact and resources">
        <a href="/contact-sales/"><small>CONTACT</small><strong>Contact sales</strong></a>
        <div aria-disabled="true"><small>HOW LICENSING WORKS</small><strong>Licensing FAQ</strong></div>
        <div aria-disabled="true"><small>COMMUNITY</small><strong>Discord</strong></div>
        <a href="https://github.com/lykeion-tech/lykeion"><small>SOURCE CODE &amp; ISSUES</small><strong>GitHub</strong></a>
      </div>
    </div></main>
    <SiteFooter/>
  </>;
}
