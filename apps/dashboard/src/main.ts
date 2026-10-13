import { createApp } from 'vue';
import { createRouter, createWebHashHistory } from 'vue-router';
import App from './App.vue';
import '@fontsource-variable/inter';
import './style.css';
import { applyTheme, savedThemeId } from './theme';
applyTheme(savedThemeId());
const router = createRouter({
  history: createWebHashHistory(),
  routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }],
});
createApp(App).use(router).mount('#app');
