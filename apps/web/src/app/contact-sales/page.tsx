'use client';

import { useState, type FormEvent } from 'react';
import Link from 'next/link';
import SiteFooter from '../site-footer';
import './contact-sales.css';

import { regions } from './regions';

export default function ContactSales() {
  const [status, setStatus] = useState<'idle' | 'sending' | 'sent' | 'error'>('idle');
  const [error, setError] = useState('');

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const body = Object.fromEntries(form.entries());
    setStatus('sending');
    try {
      const response = await fetch('/api/contact-sales', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...body, contact_consent: form.has('contact_consent'), marketing_consent: form.has('marketing_consent'), processing_consent: form.has('processing_consent') }),
      });
      if (!response.ok) {
        const message = response.status === 429 ? 'Too many requests. Please wait a little and try again.' : response.status === 400 ? 'Please check your details and use a business email address.' : 'We could not send your request. Please try again later.';
        setError(message);
        setStatus('error');
        return;
      }
      setStatus('sent');
    } catch {
      setError('We could not send your request. Please check your connection and try again.');
      setStatus('error');
    }
  }

  return <>
    <header className="site-header"><div className="nav-inner"><Link className="brand" href="/" aria-label="Lykeion home"><img src="/lykeion.svg" alt="" /></Link><div className="nav-actions"><a className="github" href="https://github.com/lykeion-tech/lykeion"><img src="/github.svg" width="17" height="17" alt="" /><span>GitHub</span></a><a className="nav-login" href="/app/#/signin">Log in</a><a className="pill primary" href="/app/#/signin">Get started</a></div></div></header>
    <main className="contact-page">
      <div className="contact-heading"><p>CONTACT SALES</p><h1>Let’s understand your needs</h1></div>
      {status === 'sent' ? <section className="contact-form contact-success" role="status"><h2>Thank you for getting in touch.</h2><p>We’ve received your request and will be in touch.</p><Link href="/">Back to Lykeion →</Link></section> :
      <form className="contact-form" onSubmit={submit}>
        <div className="contact-row">
          <label>First name <span>*</span><input name="first_name" autoComplete="given-name" maxLength={120} required /></label>
          <label>Last name <span>*</span><input name="last_name" autoComplete="family-name" maxLength={120} required /></label>
        </div>
        <div className="contact-row">
          <label>Business email <span>*</span><input name="email" type="email" autoComplete="email" maxLength={254} required aria-describedby="email-help" /><small id="email-help">Use a company or institution email. Personal email providers aren’t accepted.</small></label>
          <label>Company name <span>*</span><input name="company" autoComplete="organization" maxLength={120} required /></label>
        </div>
        <label>Company size <span>*</span><select name="company_size" defaultValue="" required><option value="" disabled>Please select</option>{['1–10', '11–50', '51–200', '201–500', '501–1,000', '1,001–5,000', '5,001+'].map(size => <option key={size} value={size}>{size}</option>)}</select></label>
        <label>Country / Region <span>*</span><select name="country" autoComplete="country-name" defaultValue="" required><option value="" disabled>Please select</option>{regions.map(region => <option key={region.code} value={region.name}>{region.name}</option>)}</select></label>
        <label>How do you plan to use or collaborate with Lykeion? <span>*</span><select name="purpose" defaultValue="" required><option value="" disabled>Please select</option><option>Use Lykeion for research</option><option>Explore Lykeion for my organization</option><option>Collaborate or partner with Lykeion</option><option>Other</option></select></label>
        <label>Your goals or challenges<textarea name="goals" maxLength={4000} rows={5} /></label>
        <div className="contact-privacy"><p>Lykeion respects your privacy. We’ll use your information to respond to your request. Please let us know if you’d also like to hear from us.</p>
          <label><input type="checkbox" name="contact_consent" /> I’d like to receive one-to-one communication from Lykeion, including service updates, support inquiries, and business-related follow-ups.</label>
          <label><input type="checkbox" name="marketing_consent" /> I’d like to receive product updates, insights, and event invitations from Lykeion.</label>
          <p>You can unsubscribe from our communications at any time. Read our <a href="/privacy/">Privacy Policy</a> for details.</p>
          <label><input type="checkbox" name="processing_consent" required /> I consent to Lykeion processing my information to respond to this request. <span>*</span></label>
        </div>
        {status === 'error' && <p className="contact-error" role="alert">{error}</p>}
        <button className="pill primary contact-submit" type="submit" disabled={status === 'sending'}>{status === 'sending' ? 'Sending…' : 'Submit'}</button>
      </form>}
    </main>
    <SiteFooter/>
  </>;
}
