import { createApp } from 'vue';
import { createRouter, createWebHashHistory } from 'vue-router';
import App from './App.vue';
import '@fontsource-variable/inter';
import './style.css';
try { const prefs=JSON.parse(localStorage.getItem('lykeion.preferences')||'{}'); document.documentElement.dataset.theme=prefs.theme&&prefs.theme!=='ash'?'dark':'light';document.documentElement.dataset.palette=prefs.theme||'ash'; } catch { document.documentElement.dataset.theme='light'; }
const router = createRouter({ history: createWebHashHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }] });
createApp(App).use(router).mount('#app');
