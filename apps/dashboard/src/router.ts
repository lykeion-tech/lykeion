import {
  createRouter,
  createWebHashHistory,
  type RouteLocationNormalized,
  type RouteRecordRaw,
} from 'vue-router';

// The board is the landing route and loads eagerly.
import TaskBoard from './views/TaskBoard.vue';
import TaskDetail from './views/TaskDetail.vue';
import ResourceView from './views/ResourceView.vue';
import SettingsView from './views/SettingsView.vue';
import SignInView from './views/SignInView.vue';
import AcceptInvite from './views/AcceptInvite.vue';
import OnboardingView from './views/OnboardingView.vue';

declare module 'vue-router' {
  interface RouteMeta {
    // Sidebar destination the route belongs to.
    page?: string;
    // Page heading; App falls back to "Studies".
    title?: string;
    // Workspace tab label when it differs from the heading.
    tabTitle?: string;
    // Shown without the sidebar and tab strip.
    bare?: boolean;
    // Reachable without a session.
    public?: boolean;
  }
}

function param(route: RouteLocationNormalized, name: string) {
  const value = route.params[name];
  return (Array.isArray(value) ? value[0] : value) || undefined;
}

// Later slices' destinations (agents, teams, skills, connectors, Autoresearch,
// Researches, Coresearch, updates) are not routed until their slice; their
// views stay in the tree.
const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/studies' },
  { path: '/signin', component: SignInView, meta: { bare: true, public: true, title: 'Sign in' } },
  {
    path: '/invite/:token',
    component: AcceptInvite,
    meta: { bare: true, title: 'Invitation' },
    props: (route) => ({ token: param(route, 'token') ?? '' }),
  },
  { path: '/onboarding', component: OnboardingView, meta: { bare: true, title: 'Set up' } },
  {
    path: '/studies/:cardId?',
    components: { default: TaskBoard, detail: TaskDetail },
    meta: { page: 'studies', title: 'Studies' },
    props: { default: false, detail: (route) => ({ id: param(route, 'cardId') ?? '' }) },
  },
  {
    path: '/inbox',
    component: ResourceView,
    meta: { page: 'inbox', title: 'Inbox' },
    props: { page: 'inbox' },
  },
  {
    path: '/colleagues',
    component: ResourceView,
    meta: { page: 'colleagues', title: 'Colleagues' },
    props: { page: 'colleagues' },
  },
  {
    path: '/machines',
    component: ResourceView,
    meta: { page: 'machines', title: 'Machines' },
    props: { page: 'runtimes' },
  },
  {
    path: '/settings/:section?',
    component: SettingsView,
    meta: { page: 'settings', title: 'Settings' },
    props: (route) => ({
      section: String(route.query.tab || param(route, 'section') || 'profile'),
    }),
  },
  { path: '/:pathMatch(.*)*', redirect: '/studies' },
];

// Case-sensitive so a miscased path is an unknown path.
export const router = createRouter({ history: createWebHashHistory(), routes, sensitive: true });
