import { QueryCache, QueryClient } from '@tanstack/vue-query';
import type { Router } from 'vue-router';
import { ApiError } from './client';
import { authenticationEpoch, authenticationLost } from './auth-loss';

// A 401 from any read means the session is gone. A refusal is never retried.
export function createDashboardQueryClient(router: Router) {
  return new QueryClient({
    queryCache: new QueryCache({
      onError(error) {
        if (
          error instanceof ApiError &&
          error.status === 401 &&
          error.authEpoch === authenticationEpoch()
        ) {
          authenticationLost(error.authEpoch);
          if (!router.currentRoute.value.meta.public) void router.replace('/signin');
        }
      },
    }),
    defaultOptions: {
      queries: {
        retry: (count, error) =>
          !(error instanceof ApiError) && error.name !== 'AbortError' && count < 2,
        staleTime: 30_000,
      },
    },
  });
}
