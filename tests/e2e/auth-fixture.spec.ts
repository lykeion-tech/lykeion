import { expect, test, request as apiRequest } from './test';
import { LAB, labCode } from './lab';

test('lab auth reset is owner-only and preserves actual IP and email limits', async ({
  request,
  baseURL,
}) => {
  const outsider = await apiRequest.newContext({
    baseURL,
    storageState: { cookies: [], origins: [] },
  });
  const reset = () => request.post(`${LAB}/lab/auth-ip/reset`);
  try {
    expect((await outsider.post(`${LAB}/lab/auth-ip/reset`)).status()).toBe(401);
    expect((await reset()).status()).toBe(204);
    const outsiderEmail = 'auth-reset-outsider@lab.test';
    expect(
      (await outsider.post('/auth/send-code', { data: { email: outsiderEmail } })).status(),
    ).toBe(204);
    expect(
      (
        await outsider.post('/auth/verify-code', {
          data: { email: outsiderEmail, code: await labCode(outsider, outsiderEmail) },
        })
      ).status(),
    ).toBe(200);
    expect((await outsider.post(`${LAB}/lab/auth-ip/reset`)).status()).toBe(403);
    expect((await reset()).status()).toBe(204);
    const email = 'auth-email-budget@lab.test';
    for (let i = 0; i < 5; i++)
      expect((await outsider.post('/auth/send-code', { data: { email } })).status()).toBe(204);
    expect((await outsider.post('/auth/send-code', { data: { email } })).status()).toBe(429);
    expect((await reset()).status()).toBe(204);
    expect((await outsider.post('/auth/send-code', { data: { email } })).status()).toBe(429);
    expect((await reset()).status()).toBe(204);
    for (let i = 0; i < 30; i++)
      expect(
        (
          await outsider.post('/auth/send-code', { data: { email: `auth-ip-${i}@lab.test` } })
        ).status(),
      ).toBe(204);
    expect(
      (
        await outsider.post('/auth/send-code', { data: { email: 'auth-ip-over@lab.test' } })
      ).status(),
    ).toBe(429);
    expect((await reset()).status()).toBe(204);
    for (let i = 0; i < 60; i++)
      expect(
        (
          await outsider.post('/auth/verify-code', {
            data: { email: 'auth-unknown@lab.test', code: '000000' },
          })
        ).status(),
      ).toBe(401);
    expect(
      (
        await outsider.post('/auth/verify-code', {
          data: { email: 'auth-unknown@lab.test', code: '000000' },
        })
      ).status(),
    ).toBe(429);
  } finally {
    await reset();
    await outsider.dispose();
  }
});
