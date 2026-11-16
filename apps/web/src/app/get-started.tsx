import Link from 'next/link';
import { ArrowRight } from 'lucide-react';

export default function GetStarted({ cta, showDemo = true }: { cta: string; showDemo?: boolean }) {
  return <section className="get-started wrap"><div><small>GET STARTED</small><h2>Give your next question<br/>to the lab.</h2><p>Open a workspace, connect a machine, and start a study.</p><a className="pill primary" href={cta}>Open your lab</a><Link className="subtle-link" href="/#use-cases">Explore Lykeion <ArrowRight size={16}/></Link></div><div className="cta-art"><div className="download-buttons"><a className="pill primary" href="/download/">Download app</a>{showDemo && <a className="pill demo-button" href="/contact-sales/">Book a demo</a>}</div></div></section>;
}
