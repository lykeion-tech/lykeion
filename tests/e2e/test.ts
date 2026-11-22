import { readFileSync } from 'node:fs';
import { test as base, type BrowserContext } from '@playwright/test';

export * from '@playwright/test';

const version: string = JSON.parse(
  readFileSync(new URL('../../package.json', import.meta.url), 'utf8'),
).version;

// Specs act as returning users who already saw this release's "What's new"
// cards, whatever account (real or mocked) they sign in as. Only the specs about
// the cards themselves import @playwright/test directly and keep the dialog.
function releaseSeen(current: string) {
  const get = Storage.prototype.getItem;
  Storage.prototype.getItem = function (this: Storage, key: string) {
    const value = get.call(this, key);
    return value === null && key.startsWith('lykeion.releaseHighlights:') ? current : value;
  };
}
const seen = (context: BrowserContext) => context.addInitScript(releaseSeen, version);

export const test = base.extend({
  context: async ({ context }, use) => {
    await seen(context);
    await use(context);
  },
  browser: [
    async ({ browser }, use) => {
      const newContext = browser.newContext.bind(browser);
      browser.newContext = async (options) => {
        const context = await newContext(options);
        await seen(context);
        return context;
      };
      await use(browser);
    },
    { scope: 'worker' },
  ],
});
