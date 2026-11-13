import type { Metadata } from 'next';

export const metadata: Metadata = { title: 'Privacy Policy · Lykeion' };

export default function PrivacyPage() {
  return (
    <main className="legal-page">
      <div className="legal-content">
        <a className="home" href="/">
          {'Lykeion'}
        </a>
        <h1>{'Privacy Policy'}</h1>
        <p className="updated">{'Last updated: 13 November 2026'}</p>
        <h2>{'Who is responsible'}</h2>
        <p>
          {
            '\n        Lykeion (app.lykeion.tech and the Lykeion desktop app) is run by Domenico Diego Marono, a private individual resident in\n        Denmark, who is the data controller under the EU General Data Protection Regulation (GDPR). For anything about\n        your data, write to '
          }
          <a href="mailto:privacy@lykeion.tech">{'privacy@lykeion.tech'}</a>
          {'.\n      '}
        </p>
        <h2>{'What we collect'}</h2>
        <ul>
          <li>
            <strong>{'Account:'}</strong>
            {
              ' your email address, the name you give, your time zone and, if you sign in with\n          Google, your Google account ID, name and email. We never receive your Google password.\n        '
            }
          </li>
          <li>
            <strong>{'Sign-in codes:'}</strong>
            {
              ' when you sign in by email we store a hashed one-time code, which expires within\n          minutes.\n        '
            }
          </li>
          <li>
            <strong>{'Your work:'}</strong>
            {
              ' the content you and your agents put in a workspace: studies, files, comments,\n          reports, records of agent runs, Coresearch projects and conversations with Aristotle.\n        '
            }
          </li>
          <li>
            <strong>{'Connected machines:'}</strong>
            {
              ' when you connect a computer, its name, operating system and which agent\n          tools it can run, and for each run the usage your agent tool reports (tokens and cost).\n        '
            }
          </li>
          <li>
            <strong>{'Technical data:'}</strong>
            {
              ' your IP address, used to limit repeated sign-in attempts and kept in our\n          server logs, which rotate and are overwritten.\n        '
            }
          </li>
        </ul>
        <p>
          {
            'We do not use advertising, analytics or tracking tools, and we do not sell or share your data for marketing.'
          }
        </p>
        <h2>{'Cookies'}</h2>
        <p>
          {
            '\n        We set only cookies needed for the service: a sign-in cookie that keeps you signed in for up to 30 days, and a\n        short-lived cookie that protects Google sign-in. Neither is used for tracking, so no consent banner is needed.\n      '
          }
        </p>
        <h2>{'Why we use it, and on what legal basis'}</h2>
        <ul>
          <li>
            {
              'To provide Lykeion to you: running your account and workspaces (GDPR Art. 6(1)(b), contract).'
            }
          </li>
          <li>
            {
              '\n          To keep the service secure and recoverable: rate limiting, logs and encrypted backups (Art. 6(1)(f),\n          legitimate interest).\n        '
            }
          </li>
          <li>{'To send you sign-in codes and service emails (Art. 6(1)(b)).'}</li>
        </ul>
        <h2>{'Who processes it for us'}</h2>
        <ul>
          <li>
            <strong>{'Hostinger International Ltd.'}</strong>
            {' hosts the server and sends our emails.'}
          </li>
          <li>
            <strong>{'Cloudflare, Inc.'}</strong>
            {
              ' stores our nightly backups in its EU region. Backups are encrypted before\n          they leave our server, with a key only we hold.\n        '
            }
          </li>
          <li>
            <strong>{'Google LLC'}</strong>
            {
              " handles Google sign-in, only if you choose it, under Google's own privacy policy.\n        "
            }
          </li>
        </ul>
        <p>
          {
            '\n        When your agents look up scientific literature, Lykeion sends their search queries, without your account\n        details, to public databases such as OpenAlex, Europe PMC and arXiv.\n      '
          }
        </p>
        <p>
          {
            '\n        Agents run on your own computer, using agent tools (for example Claude Code or Codex) signed in with your own\n        accounts. What those tools send to their providers is governed by your agreement with those providers, not by\n        this policy.\n      '
          }
        </p>
        <h2>{'How long we keep it'}</h2>
        <p>
          {
            '\n        We keep your account and workspace content while your account exists. When you delete content or ask us to\n        delete your account, it is removed from the live service within 30 days and disappears from backups when they\n        expire, which happens within a further 30 days. Server logs rotate and are overwritten automatically.\n      '
          }
        </p>
        <h2>{'Your rights'}</h2>
        <p>
          {
            '\n        You can ask to access, correct, delete or export your data, to restrict or object to its use, by writing to\n        '
          }
          <a href="mailto:privacy@lykeion.tech">{'privacy@lykeion.tech'}</a>
          {
            '. We answer within one month. You can also\n        complain to the Danish data protection authority,\n        '
          }
          <a href="https://www.datatilsynet.dk">
            {'Datatilsynet'}
          </a>
          {', or to the authority where you live.\n      '}
        </p>
        <h2>{'Age'}</h2>
        <p>{'Lykeion is not for people under 16. We do not knowingly collect data from them.'}</p>
        <h2>{'Changes'}</h2>
        <p>
          {
            '\n        If we change this policy, we update the date above and, for significant changes, tell signed-in users by email\n        before the change applies.\n      '
          }
        </p>
        <p className="foot">
          <a href="/terms/">{'Terms of Service'}</a>
        </p>
      </div>
    </main>
  );
}
