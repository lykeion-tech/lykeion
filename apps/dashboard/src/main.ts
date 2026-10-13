import { createApp } from 'vue';
import App from './App.vue';
import { router } from './router';
import '@fontsource-variable/inter';
import './style.css';
import { applyTheme, savedThemeId } from './theme';
applyTheme(savedThemeId());
createApp(App).use(router).mount('#app');
