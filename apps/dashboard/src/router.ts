import { registerDraftNavigation } from './state/resource-draft';
import {
  createRouter,
  createWebHashHistory,
  type RouteLocationNormalized,
  type RouteRecordRaw,
} from 'vue-router';

// The board is the landing route and loads eagerly.
import CoresearchList from './features/coresearch/CoresearchList.vue';
import AristotleChat from './features/coresearch/AristotleChat.vue';
import CoresearchProject from './features/coresearch/CoresearchProject.vue';
import CoresearchFileView from './features/coresearch/CoresearchFileView.vue';
import StudyBoard from './features/studies/StudyBoard.vue';
import ResearchesView from './features/projects/ResearchesView.vue';
import AutoresearchView from './features/autoresearch/AutoresearchView.vue';
import AgentsView from './features/agents/AgentsView.vue';
import AgentDetail from './features/agents/AgentDetail.vue';
import StudyDetail from './features/studies/StudyDetail.vue';
import DecisionsView from './features/decisions/DecisionsView.vue';
import ManualDecisionCreate from './features/decisions/ManualDecisionCreate.vue';
import ColleaguesView from './features/workspace/ColleaguesView.vue';
import LabChartView from './features/lab-chart/LabChartView.vue';
import TeamsView from './features/teams/TeamsView.vue';
import TeamDetail from './features/teams/TeamDetail.vue';
import TeamForm from './features/teams/TeamForm.vue';
import WorkflowsView from './features/workflows/WorkflowsView.vue';
import WorkflowDetail from './features/workflows/WorkflowDetail.vue';
import SettingsView from './features/workspace/SettingsView.vue';
import SignInView from './features/workspace/SignInView.vue';
import AcceptInvite from './features/workspace/AcceptInvite.vue';
import OnboardingView from './features/workspace/OnboardingView.vue';

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
    // The view draws its own heading instead of the shell's.
    ownHeader?: boolean;
  }
}

function param(route: Pick<RouteLocationNormalized, 'params'>, name: string) {
  const value = route.params[name];
  return (Array.isArray(value) ? value[0] : value) || undefined;
}

// Later slices' destinations stay unrouted until their slice.
const routes: RouteRecordRaw[] = [
  {
    path: '/coresearch',
    component: AristotleChat,
    meta: { page: 'coresearch', title: 'Coresearch', ownHeader: true, conversationRail: true },
  },
  {
    path: '/coresearch/new',
    component: AristotleChat,
    meta: { page: 'coresearch', title: 'New', tabTitle: 'Coresearch', ownHeader: true, conversationRail: true },
  },
  {
    path: '/coresearch/projects',
    component: CoresearchList,
    meta: { page: 'coresearch', title: 'Projects', ownHeader: true },
  },
  {
    path: '/coresearch/conversations/:conversationId',
    component: AristotleChat,
    meta: { page: 'coresearch', title: 'Coresearch', ownHeader: true, conversationRail: true },
  },
  {
    path: '/coresearch/artifacts/:projectId/:fileId',
    name: 'coresearch-artifact',
    component: CoresearchFileView,
    props: true,
    meta: { page: 'coresearch', title: 'File', ownHeader: true },
  },
  {
    path: '/coresearch/:projectId/:chatId?',
    component: CoresearchProject,
    props: true,
    meta: { page: 'coresearch', title: 'Coresearch', ownHeader: true, conversationRail: true },
  },
  { path: '/inbox/:itemId?', redirect: (route) => `/decisions/${route.params.itemId ?? ''}` },
  { path: '/updates', redirect: { path: '/settings', query: { tab: 'updates' } } },
  {
    path: '/projects',
    component: ResearchesView,
    props: true,
    meta: { page: 'projects', title: 'Projects' },
  },
  {
    path: '/projects/:id',
    component: ResearchesView,
    props: true,
    meta: { page: 'projects', title: 'Projects', ownHeader: true },
  },
  {
    path: '/autoresearch/:id?',
    component: AutoresearchView,
    props: true,
    meta: { page: 'autoresearch', title: 'Autoresearch' },
  },
  {
    path: '/agents/new',
    component: AgentsView,
    props: { create: true },
    meta: { page: 'agents', title: 'Create an agent', tabTitle: 'Agents', ownHeader: true },
  },
  { path: '/agents', component: AgentsView, meta: { page: 'agents', title: 'Agents' } },
  {
    path: '/agents/:id',
    component: AgentDetail,
    props: true,
    meta: { page: 'agents', title: 'Agents', ownHeader: true },
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
    components: { default: StudyBoard, detail: StudyDetail },
    meta: { page: 'studies', title: 'Studies' },
    props: {
      default: { scope: 'workspace' },
      detail: (route) => ({ id: param(route, 'cardId') ?? '', citation: typeof route.query.cite === 'string' ? route.query.cite : undefined }),
    },
  },
  {
    path: '/my-studies/:cardId?',
    components: { default: StudyBoard, detail: StudyDetail },
    meta: { page: 'my-studies', title: 'My Studies' },
    props: {
      default: { scope: 'mine' },
      detail: (route) => ({ id: param(route, 'cardId') ?? '', citation: typeof route.query.cite === 'string' ? route.query.cite : undefined }),
    },
  },
  {
    path: '/decisions/new',
    component: ManualDecisionCreate,
    meta: { page: 'decisions', title: 'New decision', ownHeader: true },
  },
  {
    path: '/decisions/:itemId?',
    component: DecisionsView,
    meta: { page: 'decisions', title: 'Decisions' },
    props: (route) => ({ itemId: param(route, 'itemId') }),
  },
  {
    path: '/teams/new',
    component: TeamForm,
    meta: { page: 'teams', title: 'New Team' },
  },
  {
    path: '/teams',
    component: TeamsView,
    props: true,
    meta: { page: 'teams', title: 'Teams' },
  },
  {
    path: '/teams/:id',
    component: TeamDetail,
    props: true,
    meta: { page: 'teams', title: 'Teams', ownHeader: true },
  },
  {
    path: '/workflows',
    component: WorkflowsView,
    meta: { page: 'workflows', title: 'Workflows' },
  },
  {
    path: '/workflows/:slug',
    component: WorkflowDetail,
    props: true,
    meta: { page: 'workflows', title: 'Workflows', ownHeader: true },
  },
  {
    path: '/lab-chart',
    component: LabChartView,
    meta: { page: 'lab-chart', title: 'Lykeion' },
  },
  {
    path: '/colleagues',
    component: ColleaguesView,
    meta: { page: 'colleagues', title: 'Colleagues' },
  },
  {
    path: '/colleagues/:memberId',
    component: ColleaguesView,
    meta: { page: 'colleagues', title: 'Colleagues', ownHeader: true },
  },
  {
    path: '/machines/:machineId?',
    redirect: (route) => ({
      path: '/settings',
      query: {
        tab: 'machines',
        ...(param(route, 'machineId') ? { machine: param(route, 'machineId') } : {}),
      },
    }),
  },
  {
    path: '/settings/:section?',
    component: SettingsView,
    meta: { page: 'settings', title: 'Settings' },
    props: (route) => ({
      section: String(route.query.tab || param(route, 'section') || 'profile'),
      machineId: typeof route.query.machine === 'string' ? route.query.machine : undefined,
    }),
  },
  { path: '/:pathMatch(.*)*', redirect: '/studies' },
];

// Case-sensitive so a miscased path is an unknown path.
export const router = createRouter({ history: createWebHashHistory(), routes, sensitive: true });

registerDraftNavigation(router);
