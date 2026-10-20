import { createApp } from 'vue';
import { QueryCache, QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import App from './App.vue';
import { router } from './router';
import { ApiError } from './api/client';
import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import './style.css';
import { applyTheme } from './theme';
import { usePreferences } from './state/preferences';

// A 401 from any read means the session is gone. A refusal is never retried.
const queryClient = new QueryClient({
  queryCache: new QueryCache({
    onError(error) {
      if (
        error instanceof ApiError &&
        error.status === 401 &&
        !router.currentRoute.value.meta.public
      )
        void router.replace('/signin');
    },
  }),
  defaultOptions: {
    queries: {
      retry: (count, error) => !(error instanceof ApiError) && count < 2,
      staleTime: 30_000,
    },
  },
});
applyTheme(usePreferences().theme);
createApp(App).use(router).use(VueQueryPlugin, { queryClient }).mount('#app');
