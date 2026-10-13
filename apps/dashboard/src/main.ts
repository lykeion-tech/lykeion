import { createApp } from 'vue';
import App from './App.vue';
import { router } from './router';
import '@fontsource-variable/inter';
import './style.css';
import { applyTheme } from './theme';
import { usePreferences } from './state/preferences';
applyTheme(usePreferences().theme);
createApp(App).use(router).mount('#app');
