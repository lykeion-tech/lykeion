import {
  createRouter,
  createWebHashHistory,
  type RouteLocationNormalized,
  type RouteRecordRaw,
} from 'vue-router';

// The board is the landing route and loads eagerly.
import TaskBoard from './views/TaskBoard.vue';
import ResearchesView from './views/ResearchesView.vue';
import AgentsView from './views/AgentsView.vue';
import TaskDetail from './views/TaskDetail.vue';
import MachinesView from './views/MachinesView.vue';
import InboxView from './views/InboxView.vue';
import ColleaguesView from './views/ColleaguesView.vue';
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

// Later slices' destinations (teams, skills, connectors, Autoresearch,
// Coresearch, updates) are not routed until their slice; their
// views stay in the tree.
const routes: RouteRecordRaw[] = [
  {
    path: '/projects/:id?',
    component: ResearchesView,
    props: true,
    meta: { page: 'projects', title: 'Researches' },
  },
  {
    path: '/agents/:id?',
    component: AgentsView,
    props: true,
    meta: { page: 'agents', title: 'Agents' },
  },
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
    path: '/inbox/:itemId?',
    component: InboxView,
    meta: { page: 'inbox', title: 'Inbox' },
    props: (route) => ({ itemId: param(route, 'itemId') }),
  },
  {
    path: '/colleagues',
    component: ColleaguesView,
    meta: { page: 'colleagues', title: 'Colleagues' },
  },
  {
    path: '/machines',
    component: MachinesView,
    meta: { page: 'machines', title: 'Machines' },
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
