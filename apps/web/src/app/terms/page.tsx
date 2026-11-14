import type { Metadata } from 'next';
import Link from 'next/link';

export const metadata: Metadata = { title: 'Terms of Service · Lykeion' };

export default function TermsPage() {
  return (
    <main className="legal-page">
      <div className="legal-content">
        <Link className="home" href="/">
          {'Lykeion'}
        </Link>
        <h1>{'Terms of Service'}</h1>
        <p className="updated">{'Last updated: 13 November 2026'}</p>
        <h2>{'1. Who we are'}</h2>
        <p>
          {
            '\n        Lykeion (app.lykeion.tech and the Lykeion desktop app) is provided by a private\n        individual resident in Denmark ("we"). Contact: '
          }
          <a href="mailto:privacy@lykeion.tech">{'privacy@lykeion.tech'}</a>
          {'. By\n        creating an account you accept these terms.\n      '}
        </p>
        <h2>{'2. The service'}</h2>
        <p>
          {
            '\n        Lykeion is a research platform where AI agents running on your own computer carry out scientific work that you\n        steer. It is currently free. We may change, limit or stop features, and will tell you in advance of changes\n        that significantly affect you where we reasonably can.\n      '
          }
        </p>
        <h2>{'3. Your account'}</h2>
        <p>
          {
            '\n        You must be at least 16 years old. Keep your sign-in secure; you are responsible for what happens in your\n        account and in the workspaces you manage.\n      '
          }
        </p>
        <h2>{'4. Your content'}</h2>
        <p>
          {
            '\n        You keep all rights to what you and your agents put into Lykeion. You give us permission to store, process\n        and display it only as needed to run the service for you and the people you share workspaces with. You are\n        responsible for having the right to upload what you upload, including data about other people.\n      '
          }
        </p>
        <h2>{'5. Agents and third-party tools'}</h2>
        <p>
          {
            '\n        Agents run through agent tools you install and sign in to yourself (for example Claude Code or Codex). Your\n        use of those tools, and any costs they charge, fall under your agreements with their providers.\n      '
          }
        </p>
        <p>
          {
            '\n        AI agents make mistakes. Results, citations and analyses produced in Lykeion can be wrong or incomplete, and\n        you are responsible for checking them before relying on or publishing them.\n      '
          }
        </p>
        <h2>{'6. Acceptable use'}</h2>
        <p>
          {
            "\n        Do not use Lykeion to break the law, infringe others' rights, attack or overload the service or other systems,\n        get around its security or limits, or process data you are not allowed to process. We may suspend or close\n        accounts that do, and will tell you why unless the law or security prevents it.\n      "
          }
        </p>
        <h2>{'7. Ending'}</h2>
        <p>
          {
            "\n        You can stop using Lykeion at any time and ask us to delete your account. We may end the service with at least\n        30 days' notice, during which you can export your work.\n      "
          }
        </p>
        <h2>{'8. Liability'}</h2>
        <p>
          {
            '\n        Lykeion is provided free of charge and "as is", without warranties that it will be uninterrupted or error-free.\n        To the extent the law allows, we are not liable for lost data, lost profits or indirect damage. Nothing in\n        these terms limits liability that cannot be limited by law, such as for intent or gross negligence, or rights\n        you have as a consumer that cannot be waived.\n      '
          }
        </p>
        <h2>{'9. Privacy'}</h2>
        <p>
          {'How we handle personal data is set out in our '}
          <a href="/privacy/">{'Privacy Policy'}</a>
          {'.'}
        </p>
        <h2>{'10. Changes to these terms'}</h2>
        <p>
          {
            '\n        We will tell signed-in users by email at least 15 days before significant changes apply. If you keep using\n        Lykeion after that, the new terms apply.\n      '
          }
        </p>
        <h2>{'11. Law'}</h2>
        <p>
          {
            '\n        These terms are governed by Danish law. If you are a consumer, you keep the protection of the mandatory laws of\n        the country where you live and may bring proceedings in the courts there.\n      '
          }
        </p>
        <p className="foot">
          <a href="/privacy/">{'Privacy Policy'}</a>
        </p>
      </div>
    </main>
  );
}
