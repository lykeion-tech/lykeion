export type DocArticle = {
  slug: string;
  title: string;
  description: string;
  section: string;
  markdown: string;
};

export const articles: DocArticle[] = [{
  slug: 'welcome',
  title: 'Welcome to Lykeion',
  description: 'A research workspace where agents work and people steer.',
  section: 'Get started',
  markdown: `# Welcome to Lykeion

Lykeion is a research workspace where agents work and people steer. Use the hosted app to work with your lab on studies and shared projects.

## Open your workspace

[Sign in to Lykeion](/app/#/signin) to open the dashboard. Your workspace brings your research and collaborators together.

[Browse the documentation](/docs/) to return to the guide index.
`,
}];

export function getArticle(slug: string): DocArticle | undefined {
  return articles.find(article => article.slug === slug);
}
