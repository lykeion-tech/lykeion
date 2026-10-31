import { expect, type Locator } from '@playwright/test';

// Select through the visible control, retaining support for native controls in fixtures.
export async function selectPicker(
  locator: Locator,
  choice: string | { value?: string; label?: string; index?: number },
) {
  const trigger = locator.filter({ visible: true }).first();
  await expect(trigger).toBeVisible();
  if ((await trigger.evaluate((element) => element.tagName)) === 'SELECT') {
    await trigger.selectOption(choice);
    return;
  }
  await trigger.click();
  const id = await trigger.getAttribute('aria-controls');
  if (!id) throw new Error('Picker has no controlled list');
  const list = trigger.page().locator(`[role="listbox"][id=${JSON.stringify(id)}]`);
  await expect(list).toBeVisible();
  const options = list.getByRole('option');
  if (typeof choice === 'object' && choice.index !== undefined) {
    await options.nth(choice.index).click();
  } else if (typeof choice === 'object' && choice.label !== undefined) {
    await list.getByRole('option', { name: choice.label, exact: true }).click();
  } else {
    const value = typeof choice === 'string' ? choice : (choice.value ?? '');
    await list.locator(`[role="option"][data-value=${JSON.stringify(value)}]`).click();
  }
}
