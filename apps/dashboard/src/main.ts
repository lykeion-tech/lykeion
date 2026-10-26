import { createApp } from 'vue';
import { VueQueryPlugin } from '@tanstack/vue-query';
import App from './App.vue';
import { router } from './router';
import { createDashboardQueryClient } from './api/query-client';
import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import './style.css';
import { applyTheme } from './theme';
import { usePreferences } from './state/preferences';

const queryClient = createDashboardQueryClient(router);
applyTheme(usePreferences().theme);
createApp(App).use(router).use(VueQueryPlugin, { queryClient }).mount('#app');
